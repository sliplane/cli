// Progress formatting handles empty and unknown lengths without inventing a percentage.
package cli

import (
	"bytes"
	"strings"
	"testing"
)

// TestDownloadProgressValues checks byte units, unknown totals, and completion after throttled updates.
func TestDownloadProgressValues(t *testing.T) {
	for _, test := range []struct {
		written, total int64
		want           string
	}{
		{512, 1024, "512 B / 1.0 KiB  (50%)"},
		{1048576, 2097152, "1.0 MiB / 2.0 MiB  (50%)"},
		{0, 0, "0 B / 0 B  (100%)"},
		{1024, -1, "Downloading  1.0 KiB"},
	} {
		var output bytes.Buffer
		progress := downloadProgress{output: &output}
		if err := progress.update(test.written, test.total); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(output.String(), test.want) {
			t.Fatalf("progress %q does not contain %q", output.String(), test.want)
		}
		if test.total < 0 && strings.Contains(output.String(), "%") {
			t.Fatal("unknown length displayed a percentage")
		}
		if test.total > 0 {
			if err := progress.update(test.total, test.total); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(output.String(), "100%") {
				t.Fatal("completion was throttled")
			}
		}
	}
}
