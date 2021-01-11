FROM golang:latest

ENV SHELL /bin/bash
ENV HOME /root
WORKDIR $HOME

COPY . /go/src/github.tamu.edu/April1989/go_tools/cmd/getgo

RUN ( \
		cd /go/src/github.tamu.edu/April1989/go_tools/cmd/getgo \
		&& go build \
		&& mv getgo /usr/local/bin/getgo \
	)

# undo the adding of GOPATH to env for golibexec_testing
ENV PATH /usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
ENV GOPATH ""

# delete /go and /usr/local/go for golibexec_testing
RUN rm -rf /go /usr/local/go
