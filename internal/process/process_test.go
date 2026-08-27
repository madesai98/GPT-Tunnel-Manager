package process

import "testing"

func TestInvokeLineCallbackContainsPanic(t *testing.T) {
	called := false
	invokeLineCallback(func(Line) {
		called = true
		panic("third-party log callback failure")
	}, Line{Stream: "stderr", Text: "boom"})
	if !called {
		t.Fatal("callback was not invoked")
	}
}
