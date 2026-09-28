package ssmmgr

import "testing"

func TestConnectRejectsInjection(t *testing.T) {
	cases := [][3]string{
		{"p", "eu-west-1", "i-1&calc"},
		{"p", "eu-west-1", "i-0123456789abcdef0 & calc"},
		{"p", "eu-west-1&calc", "i-0123456789abcdef0"},
		{"p&calc", "eu-west-1", "i-0123456789abcdef0"},
	}
	for _, c := range cases {
		if err := Connect(c[0], c[1], c[2]); err == nil {
			t.Errorf("Connect(%q) accepted", c)
		}
	}
}

func TestQuoting(t *testing.T) {
	if got := shellQuote("a'b"); got != `'a'\''b'` {
		t.Errorf("shellQuote: %s", got)
	}
	if got := appleScriptQuote(`a\b"c`); got != `"a\\b\"c"` {
		t.Errorf("appleScriptQuote: %s", got)
	}
	if got := quoteWindows("a b"); got != `"a b"` {
		t.Errorf("quoteWindows: %s", got)
	}
}
