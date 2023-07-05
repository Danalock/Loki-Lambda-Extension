# Loki Lambda Extension

This extension: 
* Based on AWS Logs API Extension example https://github.com/aws-samples/aws-lambda-extensions/tree/main/go-example-logs-api-extension
* Subscribes to recieve platform and function logs
* Runs with a main and a helper goroutine: The main goroutine registers to ExtensionAPI and process its invoke and shutdown events (see nextEvent call). The helper goroutine:
    - starts a local HTTP server at the provided port (default 1234) that receives requests from Logs API
    - puts the logs in a synchronized queue (Producer) to be processed by the main goroutine (Consumer)
* Writes the received logs to Loki via Push API using protobuf

Note that current implementation always tries to push all the buffered logs at the end of the invocation. This means that there will be some additional time amount needed after the lambda invocation is completed to flush the logs. This should not slow down Lambda response time, but it will add to the billed time. One should look into LOKI_BUFFER_TIMEOUT_MS environment variable to set the most appropriate value for the lambda to optimize this. This implementation makes most sense for Lambdas that don't get invocated to a very high degree (~ <10 times a second).

## Extension (itself) logs

Extension is setup to log function and platform logs only. If you need the logs coming from the extension itself, one still needs to look into CloudWatch logs (if enabled) for that or change the implementation to subscribe to extension logs as well. However one should be careful with extension logs to not cause infinite log loops.

## Development and Testing

deploy_custom_and_apply can be used to quickly deploy the layer and add it to a lambda, for example:
```console
$ ./deploy_custom_and_apply.sh temp-lambda-layer eu-west-1 sandbox my-test-lambda
```

## Environment variables

* LOKI_ENDPOINT_URL: required, LOKI endpoint that supports Push API (/loki/api/v1/push)
* LOKI_APPLICATION_LABEL: required
* LOKI_ENV_LABEL: required
* LOKI_BEARER_TOKEN: required if LOKI_USERNAME & LOKI_PASSWORD aren't provided
* LOKI_USERNAME && LOKI_PASSWORD: required if LOKI_BEARER_TOKEN is not provided. Untested
* LOKI_BUFFER_TIMEOUT_MS: how long should lambda runtime buffer logs before sending to the extension, default here is 200ms. (min: 25, max: 30000)
* LOKI_EXTRA_LABELS: example: "mylabel1,myvalue1,mylabel2,myvalue2"
* LOKI_DEBUG: set to true to get extra logs from the extension itself

LOKI_BUFFER_TIMEOUT_MS - in general this should match or be slightly higher than the expected lambdas invocation time. Though this rule may not exactly apply to long runnning lambda instances (60s+), then one might to have a lower timeout value anyway.

## Labels

These are the labels that extension pushes to the Loki:
* application - value provided by LOKI_APPLICATION_LABEL env var
* environment - value provided by LOKI_ENV_LABEL env var
* lambda - value will be either 'function' or 'platform' based on what sort of log it is

## Potential future improvements

* It would be nice if extension would detect that if it is being invocated many times. Then if so it would not wait to flush the logs on each invocation, but instead buffer the logs and send them every X seconds or so. This could greatly improve the log efficiency for busy lambdas.
