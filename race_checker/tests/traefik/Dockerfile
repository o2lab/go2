# This Dockerfile sets up the build environment for traefic (github.com/traefik/traefik).
# 
# Usage:
#   $ docker build -t o2lab/traefic-bench:latest .
#   $ docker run -it traefuc-bench
# Then we are ready to run our race checker on traefic:
#   $ race-checker <options> .

from golang:buster

# Use a machine account to access our private repo.
ARG github_token=a94dc614ddce979090b54f5ead186471fb8b37d6 

RUN git clone https://${github_token}:x-oauth-basic@github.com/o2lab/go2.git /home/go2
RUN cd /home/go2/race_checker && go build
ENV PATH="/home/go2/race_checker:${PATH}"

RUN git clone https://github.com/traefik/traefik.git /go/src/github.com/traefik/traefik
WORKDIR /go/src/github.com/traefik/traefik
RUN GO111MODULE=off go get -u github.com/containous/go-bindata/...
RUN go generate

WORKDIR /go/src/github.com/traefik/traefik/cmd/traefik
