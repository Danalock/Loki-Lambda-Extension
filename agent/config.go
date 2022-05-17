package agent

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/prometheus/common/model"
	"github.com/sirupsen/logrus"
)

func SetupArguments() {
	addr := getRequiredEnvVar("LOKI_ENDPOINT_URL") + "/loki/api/v1/push"

	var err error
	writeAddress, err = url.Parse(addr)
	if err != nil {
		panic(err)
	}

	applicationLabel := getRequiredEnvVar("LOKI_APPLICATION_LABEL")
	envLabel := getRequiredEnvVar("LOKI_ENV_LABEL")
	labels = model.LabelSet{
		model.LabelName("application"): model.LabelValue(applicationLabel),
		model.LabelName("environment"): model.LabelValue(envLabel),
	}

	extraLabelsRaw = os.Getenv("LOKI_EXTRA_LABELS")
	var extraLabels model.LabelSet
	extraLabels, err = parseExtraLabels(extraLabelsRaw)
	if err != nil {
		panic(err)
	}

	labels = labels.Merge(extraLabels)

	bearerToken = os.Getenv("LOKI_BEARER_TOKEN")

	username = os.Getenv("LOKI_USERNAME")
	password = os.Getenv("LOKI_PASSWORD")
	// If either username or password is set then both must be.
	if (username != "" && password == "") || (username == "" && password != "") {
		panic("both username and password must be set if either one is set")
	}

	batch := os.Getenv("LOKI_BATCH_SIZE")
	batchSize = 131072
	if batch != "" {
		batchSize, _ = strconv.Atoi(batch)
	}

	bufferTimeoutEnvValue := os.Getenv("LOKI_BUFFER_TIMEOUT_MS")
	bufferTimeoutMs = 200
	if bufferTimeoutEnvValue != "" {
		bufferTimeoutMs, _ = strconv.Atoi(bufferTimeoutEnvValue)
	}

	debug := envOrElseBool("LOKI_DEBUG", false)
	logLevel := logrus.InfoLevel
	if debug {
		logLevel = logrus.DebugLevel
	}
	logrus.SetLevel(logLevel)
}

func envOrElseBool(key string, fallback bool) bool {
	if value, ok := os.LookupEnv(key); ok {
		v, err := strconv.ParseBool(value)
		if err != nil {
			return fallback
		}
		return v
	}
	return fallback
}

func getRequiredEnvVar(envVarName string) string {
	label := os.Getenv(envVarName)
	if label == "" {
		panic(errors.New(fmt.Sprintf("Required environmental variable '%s' was not found", envVarName)))
	}

	return label
}

func parseExtraLabels(extraLabelsRaw string) (model.LabelSet, error) {
	var extractedLabels = model.LabelSet{}
	extraLabelsSplit := strings.Split(extraLabelsRaw, ",")

	if len(extraLabelsRaw) < 1 {
		return extractedLabels, nil
	}

	if len(extraLabelsSplit)%2 != 0 {
		return nil, fmt.Errorf(invalidExtraLabelsError)
	}
	for i := 0; i < len(extraLabelsSplit); i += 2 {
		extractedLabels[model.LabelName(extraLabelsSplit[i])] = model.LabelValue(extraLabelsSplit[i+1])
	}
	err := extractedLabels.Validate()
	if err != nil {
		return nil, err
	}
	return extractedLabels, nil
}
