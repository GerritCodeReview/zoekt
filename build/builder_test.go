package build

import "testing"

func TestOptions(t *testing.T) {
	t.Run("LargeFiles", func(t *testing.T) {
		opts := Options{
			LargeFiles: "foo, .bar, foo.bar, .foo.bar, *.foo, ",
		}

		tests := []struct {
			name   string
			ignore bool
		}{
			{name: "/foo", ignore: true},
			{name: "/foo/foo", ignore: true},
			{name: "/foo/bar", ignore: false},
			{name: "/bar/foo", ignore: true},
			{name: "/foo/bar/foo", ignore: true},
			{name: "/foo/bar/.foo", ignore: true}, // matches *.foo
			{name: "/foo/.bar", ignore: true},
			{name: "/foo/bar/foo.bar", ignore: true},
			{name: "/foo/bar/.foo.bar", ignore: true},
			{name: "/foo/bar.foo", ignore: true},
			{name: "/foo/bar.foo", ignore: true},
		}

		for _, test := range tests {
			ignore := opts.IgnoreSizeMax(test.name)
			if ignore != test.ignore {
				t.Errorf("unexpected result for name %s: got %v, want %v", test.name, ignore, test.ignore)
			}
		}
	})
}

