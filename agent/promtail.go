package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/gogo/protobuf/proto"
	"github.com/golang/snappy"
	"github.com/grafana/dskit/backoff"
	"github.com/grafana/loki/pkg/logproto"
	"github.com/prometheus/common/model"
	log "github.com/sirupsen/logrus"
	"gitlab.danalockapps.com/Backend/loki-lambda-extension/logsapi"
)

const (
	timeout    = 5 * time.Second
	minBackoff = 100 * time.Millisecond
	maxBackoff = 30 * time.Second
	maxRetries = 10

	// We use snappy-encoded protobufs over http by default.
	contentType             = "application/x-protobuf"
	maxErrMsgLen            = 1024
	invalidExtraLabelsError = "Invalid value for environment variable EXTRA_LABELS. Expected a comma seperated list with an even number of entries. "
)

var logger = log.WithFields(log.Fields{"agent": "logsApiAgent"})

var (
	writeAddress                                    *url.URL
	username, password, extraLabelsRaw, bearerToken string
	batchSize, bufferTimeoutMs                      int
	labels                                          model.LabelSet
)

type entry struct {
	labels model.LabelSet
	entry  logproto.Entry
}

type batch struct {
	streams map[string]*logproto.Stream
	size    int
}

type Logger interface {
	SendLog(log string) error
}

type LogMessage struct {
	Type   string      `json:"type"`
	Time   string      `json:"time"`
	Record interface{} `json:"record"`
}

// LokiLogger is the logger that writes the logs received from Logs API to Loki
type LokiLogger struct {
	ctx context.Context
}

// NewLogger returns a Loki Logger
func NewLogger(ctx context.Context) (*LokiLogger, error) {
	return &LokiLogger{
		ctx: ctx,
	}, nil
}

// SendLog sends given logs to Loki
func (l *LokiLogger) SendLog(log string) error {
	var logs []LogMessage
	err := json.Unmarshal([]byte(log), &logs)
	if err != nil {
		return err
	}

	batch, err := newBatch(l.ctx)
	if err != nil {
		return err
	}

	for _, msg := range logs {
		var logStr []byte
		var err error
		if msg.Type == string(logsapi.Function) {
			logStr, err = json.Marshal(msg.Record)
		} else {
			logStr, err = json.Marshal(msg)
		}
		if err != nil {
			return err
		}
		logger.Debug("Preparing to send log message: " + string(logStr))

		batch.add(l.ctx, entry{labels, logproto.Entry{
			Line:      string(logStr),
			Timestamp: parseMessageTimestamp(msg),
		}})
	}

	err = sendToPromtail(l.ctx, batch)
	if err != nil {
		logger.Warn(err)
	}

	return nil
}

// parseMessageTimestamp is a helper function that tries to parse the timestamp from the
// log event payload. If it cannot parse the timestamp, it returns the current timestamp.
func parseMessageTimestamp(msg LogMessage) time.Time {
	logger.Debug("parseMessageTimestamp")
	ts, err := time.Parse(time.RFC3339, msg.Time)
	if err != nil {
		return time.Now()
	}
	return ts
}

func newBatch(ctx context.Context, entries ...entry) (*batch, error) {
	b := &batch{
		streams: map[string]*logproto.Stream{},
	}

	for _, entry := range entries {
		err := b.add(ctx, entry)
		return b, err
	}

	return b, nil
}

func (b *batch) add(ctx context.Context, e entry) error {
	labels := labelsMapToString(e.labels)
	stream, ok := b.streams[labels]
	if !ok {
		b.streams[labels] = &logproto.Stream{
			Labels:  labels,
			Entries: []logproto.Entry{},
		}
		stream = b.streams[labels]
	}

	stream.Entries = append(stream.Entries, e.entry)
	b.size += len(e.entry.Line)

	if b.size > batchSize {
		return b.flushBatch(ctx)
	}

	return nil
}

func labelsMapToString(ls model.LabelSet, without ...model.LabelName) string {
	lstrs := make([]string, 0, len(ls))
Outer:
	for l, v := range ls {
		for _, w := range without {
			if l == w {
				continue Outer
			}
		}
		lstrs = append(lstrs, fmt.Sprintf("%s=%q", l, v))
	}

	sort.Strings(lstrs)
	return fmt.Sprintf("{%s}", strings.Join(lstrs, ", "))
}

func (b *batch) encode() ([]byte, int, error) {
	req, entriesCount := b.createPushRequest()
	buf, err := proto.Marshal(req)
	if err != nil {
		return nil, 0, err
	}

	buf = snappy.Encode(nil, buf)
	return buf, entriesCount, nil
}

func (b *batch) createPushRequest() (*logproto.PushRequest, int) {
	req := logproto.PushRequest{
		Streams: make([]logproto.Stream, 0, len(b.streams)),
	}

	entriesCount := 0
	for _, stream := range b.streams {
		req.Streams = append(req.Streams, *stream)
		entriesCount += len(stream.Entries)
	}
	return &req, entriesCount
}

func (b *batch) flushBatch(ctx context.Context) error {
	err := sendToPromtail(ctx, b)
	if err != nil {
		return err
	}

	b.streams = make(map[string]*logproto.Stream)

	return nil
}

func sendToPromtail(ctx context.Context, b *batch) error {
	buf, _, err := b.encode()
	if err != nil {
		return err
	}

	backoff := backoff.New(ctx, backoff.Config{
		MinBackoff: minBackoff,
		MaxBackoff: maxBackoff,
		MaxRetries: maxRetries,
	})
	var status int
	for {
		logger.Debug("Going to send to Loki... Retry count: %d\n", backoff.NumRetries())

		// send uses `timeout` internally, so `context.Background` is good enough.
		status, err = send(context.Background(), buf)

		// Only retry 429s, 500s and connection-level errors.
		if status > 0 && status != 429 && status/100 != 5 {
			logger.Debug("Send to Loki went well")
			break
		}

		fmt.Printf("error sending batch, will retry, status: %d error: %s\n", status, err)
		backoff.Wait()

		// Make sure it sends at least once before checking for retry.
		if !backoff.Ongoing() {
			break
		}
	}

	if err != nil {
		fmt.Printf("Failed to send logs! %s\n", err)
		return err
	}

	return nil
}

func send(ctx context.Context, buf []byte) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequest("POST", writeAddress.String(), bytes.NewReader(buf))
	if err != nil {
		return -1, err
	}
	req.Header.Set("Content-Type", contentType)

	if bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+bearerToken)
	} else if username != "" && password != "" {
		req.SetBasicAuth(username, password)
	}

	resp, err := http.DefaultClient.Do(req.WithContext(ctx))
	if err != nil {
		return -1, err
	}

	if resp.StatusCode/100 != 2 {
		scanner := bufio.NewScanner(io.LimitReader(resp.Body, maxErrMsgLen))
		line := ""
		if scanner.Scan() {
			line = scanner.Text()
		}
		err = fmt.Errorf("server returned HTTP status %s (%d): %s", resp.Status, resp.StatusCode, line)
	}

	return resp.StatusCode, err
}
