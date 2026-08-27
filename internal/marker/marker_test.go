package marker

import "testing"

func TestGenerateOneLineAppDescription(t *testing.T) {
	id := "srv_0123456789abcdef0123456789abcdef"
	want := "GTM PLUGIN | " + id + " | Follow the gpt-tunnel-manager-lifecycle skill before using this plugin"
	if got := Generate(id); got != want {
		t.Fatalf("Generate() = %q, want %q", got, want)
	}
}

func TestRoundTrip(t *testing.T) {
	id := "srv_0123456789abcdef0123456789abcdef"
	got, err := Parse(Generate(id))
	if err != nil || got != id {
		t.Fatalf("got %q err %v", got, err)
	}
}

func TestParseEmbeddedDescription(t *testing.T) {
	id := "srv_0123456789abcdef0123456789abcdef"
	description := "Extra text. " + Generate(id) + " More text."
	got, err := Parse(description)
	if err != nil || got != id {
		t.Fatalf("got %q err %v", got, err)
	}
}
