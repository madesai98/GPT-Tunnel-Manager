package retrieval

import "testing"

func TestLexicalIndexBM25RankingAndStableTies(t *testing.T) {
	index, err := NewLexicalIndex([]LexicalDocument{
		{Key: "weather", Text: "weather forecast temperature rain"},
		{Key: "mail", Text: "send email message recipient"},
		{Key: "alpha", Text: "shared token"},
		{Key: "beta", Text: "shared token"},
	})
	if err != nil {
		t.Fatal(err)
	}
	results, err := index.Search("weather forecast", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Key != "weather" {
		t.Fatalf("weather results = %#v", results)
	}
	results, err = index.Search("shared", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || results[0].Key != "alpha" || results[1].Key != "beta" {
		t.Fatalf("tie results = %#v", results)
	}
	results, err = index.Search("absent-term", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 0 {
		t.Fatalf("no-match results = %#v", results)
	}
}

func TestTokenizeIsDeterministicUnicodeAware(t *testing.T) {
	got := Tokenize("HTTP_Server v2 — Café42")
	want := []string{"http", "server", "v2", "café42"}
	if len(got) != len(want) {
		t.Fatalf("tokens = %#v", got)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("tokens = %#v", got)
		}
	}
}
