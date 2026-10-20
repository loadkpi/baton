// Package test holds the tests of Baton. They all use the one seam of the
// project: the baton executable run as a subprocess (see internal/batontest).
package test

import (
	"fmt"
	"os"
	"testing"

	"github.com/loadkpi/baton/internal/batontest"
)

// batonBin is the binary built once for the whole test run.
var batonBin string

func TestMain(m *testing.M) {
	bin, cleanup, err := batontest.BuildBinary()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	batonBin = bin

	code := m.Run()
	cleanup()
	os.Exit(code)
}

// newEnv is the first line of every test: an isolated HOME, a source git
// repository and recording stubs on PATH.
func newEnv(t *testing.T) *batontest.Env {
	t.Helper()
	return batontest.New(t, batonBin)
}
