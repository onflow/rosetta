FROM 652969937640.dkr.ecr.us-east-1.amazonaws.com/containers/golang:v1-17 AS rosetta-builder

RUN mkdir -p /app \
  && chown -R nobody:nogroup /app
WORKDIR /app

ARG DEBIAN_FRONTEND=noninteractive
ENV TZ=Etc/UTC

RUN apt-get update
RUN apt-get -y install cmake

RUN cd /app && mkdir -p src

COPY . /app/src/rosetta-flow
RUN cd /app/src/rosetta-flow && go mod download -x
RUN cd /app/src/rosetta-flow && ./environ/build-relic.py
RUN cd /app/src/rosetta-flow && go build -tags relic -o server cmd/server/server.go

CMD ["/app/src/rosetta-flow/server", "/app/src/rosetta-flow/mainnet.json"]
