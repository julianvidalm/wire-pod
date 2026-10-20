package initwirepod

import "testing"

func TestRecordMaxBytes(t *testing.T) {
	for _, c := range []struct {
		env  string
		want int64
		err  bool
	}{
		{"", 5 << 30, false},
		{"5", 5 << 30, false},
		{"0.5", 1 << 29, false},
		{"0", 0, false},
		{"-1", 5 << 30, true},
		{"lots", 5 << 30, true},
	} {
		got, err := recordMaxBytes(c.env)
		if got != c.want || (err != nil) != c.err {
			t.Errorf("%q: got %d, %v; want %d, error %v", c.env, got, err, c.want, c.err)
		}
	}
}
