FROM buildpack-deps:buster-scm

# gcc for cgo
RUN apt-get update && apt-get install -y --no-install-recommends \
		g++ \
		gcc \
		libc6-dev \
		make \
		pkg-config \
		vim \
		ncat \
	&& rm -rf /var/lib/apt/lists/*

RUN set -eux; \
	\
	url="https://github.com/dopelsunce/go/releases/download/neo/dist.tar.gz"; \
	wget -O go.tgz "$url"; \
	tar -C /usr/local -xzf go.tgz; \
	rm go.tgz; \
    \
	export PATH="/usr/local/go/bin:$PATH"; \
	go version

ENV GOPATH /go
ENV GOROOT /usr/local/go
ENV CC gcc
ENV CXX g++
ENV PATH $GOPATH/bin:/usr/local/go/bin:$PATH

RUN mkdir -p "$GOPATH/src" "$GOPATH/bin" && chmod -R 777 "$GOPATH"
WORKDIR $GOPATH

RUN git clone --depth 1 https://github.com/kubernetes/kubernetes.git
RUN git clone --depth 1 https://github.com/etcd-io/etcd.git
RUN git clone --depth 1 https://github.com/grpc/grpc-go.git
RUN git clone --depth 1 https://github.com/cockroachdb/cockroach.git
RUN git clone --depth 1 https://github.com/prometheus/prometheus.git
RUN git clone --depth 1 https://github.com/pingcap/tidb.git
RUN git clone --depth 1 https://github.com/syncthing/syncthing.git
RUN git clone --depth 1 https://github.com/gogs/gogs.git
RUN git clone --depth 1 https://github.com/moby/moby.git
RUN git clone --depth 1 https://github.com/ethereum/go-ethereum.git
#RUN git clone --depth 1 https://github.com/caddyserver/caddy.git
#RUN git clone --depth 1 https://github.com/astaxie/beego.git

COPY test_one.sh /go
RUN /bin/bash -c "mkdir /go/log && source test_one.sh && golist"