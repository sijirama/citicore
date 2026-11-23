package chat

import "testing"

func TestValidQuote(t *testing.T) {
	passage := "Every person shall be entitled to freedom of expression, including freedom to hold opinions and to receive and impart ideas and information without interference."
	for _, test := range []struct {
		quote string
		want  bool
	}{
		{"freedom of expression, including freedom to hold opinions", true},
		{"Freedom of expression including freedom to hold opinions", true},
		{"freedom of expression and association", false},
		{"freedom", false},
	} {
		if got := validQuote(passage, test.quote); got != test.want {
			t.Errorf("validQuote(%q) = %v, want %v", test.quote, got, test.want)
		}
	}
}
