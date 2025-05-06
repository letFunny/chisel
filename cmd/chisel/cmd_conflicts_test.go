package main_test

import (
	. "gopkg.in/check.v1"

	chisel "github.com/canonical/chisel/cmd/chisel"
)

type conflictTest struct {
	a, b string
	cnfl string
}

var conflictTests = []conflictTest{{
	a:    "/bin/foo",
	b:    "/bin/bar",
	cnfl: "/bin",
}, {
	a:    "/dir/*/foo",
	b:    "/dir/bar/bar",
	cnfl: "/dir",
}, {
	a:    "/*/foo",
	b:    "/dir/bar",
	cnfl: "/*",
}, {
	a:    "/*a/foo",
	b:    "/dir/bar",
	cnfl: "",
}, {
	a:    "/*r/foo",
	b:    "/dir/bar",
	cnfl: "/*r",
}, {
	a:    "/d?r/foo",
	b:    "/dir/bar",
	cnfl: "/d?r",
}, {
	a:    "/dir?/foo",
	b:    "/dir/bar",
	cnfl: "",
}, {
	a:    "/dir*/foo",
	b:    "/d*/bar",
	cnfl: "/dir*",
}, {
	a:    "/**",
	b:    "/dir/bar",
	cnfl: "/**",
}, {
	a:    "/d**r",
	b:    "/dir/bar",
	cnfl: "/d**",
}, {
	a:    "/d**r",
	b:    "/foo/bar",
	cnfl: "",
}, {
	a:    "/dir/*/foo",
	b:    "/xyz/**",
	cnfl: "",
}}

func (s *ChiselSuite) TestConflicts(c *C) {
	for _, test := range conflictTests {
		s := chisel.HasConflict(test.a, test.b)
		c.Assert(s, Equals, test.cnfl)
	}
}
