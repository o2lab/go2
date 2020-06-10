# Get base image
FROM buildpack-deps:buster-scm

# Clone private GitHub Repository
RUN git clone -b docker https://<02a74b1c46a4662d170cc2707327db67bec65d7b>:x-oauth-basic@github.tamu.edu/jeffhuang/go2.git /race_checker/
RUN cp -R /race_checker/* /home/app/
RUN chown app:app -R /home/app/

ENV GOPATH /go2
ENV GOROOT /usr/local/go2
ENV CC gcc
ENV CXX g++
ENV PATH $GOPATH/bin:/usr/local/go/bin:$PATH

RUN mkdir -p "$GOPATH/src" "$GOPATH/bin" && chmod -R 777 "$GOPATH"
WORKDIR $GOPATH

CMD ["/sbin/my_init"]