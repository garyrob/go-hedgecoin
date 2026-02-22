# Tasks for go-hedgecoin



## accept (min)

> Runs the acceptance test with 5 nodes (and a relay node) with one of the nodes having a weight supplied by the daemon of 1.5 and the others 1.

```sh
cd /Users/garyrob/Source/go-hedgecoin
unset CGO_CFLAGS
unset CGO_LDFLAGS
export NODEBINDIR=$HOME/go/bin
export TESTDATADIR=$(pwd)/test/testdata
export TESTDIR=/tmp
export WEIGHT_TEST_DURATION=$((min))m
go clean -cache
go test -v ./test/e2e-go/features/weightoracle/... -run TestWeightedConsensus -timeout $((min+5))m

```


