FROM ubuntu:22.04

RUN apt-get update -y
RUN apt-get install -y cmake
RUN apt-get install -y golang-go

RUN mkdir -p /app && chown -R nobody:nogroup /app
WORKDIR /app
RUN cd /app && mkdir -p src
COPY . /app/src/rosetta-flow
RUN cd /app/src/rosetta-flow && go mod download -x
RUN cd /app/src/rosetta-flow && ./environ/build-relic.py
RUN cd /app/src/rosetta-flow && go build -tags relic -o server cmd/server/server.go

CMD ["/app/src/rosetta-flow/server", "/app/src/rosetta-flow/mainnet.json"]
