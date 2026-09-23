package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

// TestConfirm requires an affirmative answer and treats Enter and EOF as a decline.
func TestConfirm(t *testing.T) {
	for _, test := range []struct {
		input string
		want  bool
	}{
		{"y\n", true}, {" YES \n", true}, {"no\n", false},
		{"\n", false}, {"", false}, {"y", false}, {"maybe\ny\n", true},
	} {
		t.Run(test.input, func(t *testing.T) {
			var output bytes.Buffer
			got, err := confirm(context.Background(), strings.NewReader(test.input), &output, "Connect?")
			if err != nil || got != test.want || !strings.Contains(output.String(), "[y/N]") {
				t.Fatalf("confirm = %v, %v; prompt %q", got, err, output.String())
			}
		})
	}
}

// TestConfirmCancellation exits while stdin remains open without a submitted answer.
func TestConfirmCancellation(t *testing.T) {
	input, writer := io.Pipe()
	defer input.Close()
	defer writer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	confirmed, err := confirm(ctx, input, io.Discard, "Connect?")
	if confirmed || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("confirmation ignored cancellation: %v, %v", confirmed, err)
	}
}
