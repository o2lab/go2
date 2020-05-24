testgo() {
  COUNT=${COUNT:-1}
  TIMEOUT=${TIMEOUT:-10s}
  OUT=${OUT:-log}
  echo running "go test -race -json -count $COUNT -timeout $TIMEOUT ./... >../${OUT}/$1.$$"
  go test -race -json -count $COUNT -timeout $TIMEOUT ./... >../${OUT}/$1.$$
}

test_one() {
  p=$1
  echo testing $p. output: ${p}.$$
  (cd $p; testgo $p)
}

declare -a projects=(kubernetes caddy beego prometheus tidb gogs grpc-go syncthing)

test_all() {
  for p in "${projects[@]}"; do
    test_one $p
  done
}

golist() {
  for p in "${projects[@]}"; do
    (cd $p; go list ./...)
  done
}

switch_race() {
  (cd $GOROOT/src/runtime/race; ./switch_race_syso.sh $1)
}

show_cur_race() {
  (cd $GOROOT/src/runtime/race; ls -lh race_linux_amd64.syso)
}

test_race() {
    (cd $GOROOT/src/runtime/race; go test -v -race race_test.go)
}

release_dist() {
  src=${1:-go-pred}
  rm -rf go-dist
  mkdir go-dist
  echo copying src files in $src
  rsync -a --exclude=".*" $GOROOT go-dist
  (cd go-dist; mv $src go; tar czf dist.tar.gz go;
  echo uploading dist.tar.gz
  github-release upload \
    --user dopelsunce \
    --repo go \
    --tag neo \
    --name dist.tar.gz \
    --replace \
    --file dist.tar.gz)
}

update_neo() {
  dst=${1:-neo-ufo}
  (cd $dst/compiler-rt/lib/tsan/go; \
   git pull; \
   CC=clang-9 ./buildgo.sh; \
   cp race_linux_amd64.syso $GOROOT/src/runtime/race/race_linux_amd64.syso.neo)
}

update_dist() {
  url="https://github.com/dopelsunce/go/releases/download/neo/dist.tar.gz"
	wget -O go.tgz "$url"
	tar -C /usr/local -xzf go.tgz
	rm go.tgz
	go version
}