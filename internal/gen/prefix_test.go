package gen

import "testing"

func TestCheckPrefix(t *testing.T) {
	for _, c := range []struct {
		prefix string
		paths  []string
		bad    bool
	}{
		{"/api/account/v1", []string{"/user", "/v1/user"}, true},
		{"/api/account", []string{"/v1/user"}, false},
		{"/api/account/v1", []string{"/user", "/v2/x"}, false},
		{"", []string{"/v1/user"}, false},
	} {
		if err := checkPrefix(c.prefix, c.paths); (err != nil) != c.bad {
			t.Errorf("checkPrefix(%q, %v) = %v, want error %v", c.prefix, c.paths, err, c.bad)
		}
	}
}
