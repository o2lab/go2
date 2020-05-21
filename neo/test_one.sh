testgo() {
    go test -race -json -count 1 -timeout 10s ./...
}

test_one() {
  p=$1
  echo testing $p. output: ${p}.$$
  (cd $p; testgo >../log/${p}.$$)
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
