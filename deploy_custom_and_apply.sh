#!/bin/bash

GOOS=linux GOARCH=amd64 go build -o extensions/extension && chmod +x extensions/extension
zip -r extension.zip extensions/
layer_arn=$(aws lambda publish-layer-version --layer-name "$1" --compatible-runtimes \
      nodejs \
      nodejs14.x \
      python3.6 \
      python3.7 \
      python3.8 \
      python3.9 \
      dotnet6 \
      go1.x \
      provided \
      provided.al2 \
    --compatible-architectures x86_64 --region $2 --profile $3 --zip-file  "fileb://extension.zip" | jq -r '.LayerVersionArn')
aws lambda update-function-configuration --region $2 --profile $3 --function-name $4 --layers $layer_arn
