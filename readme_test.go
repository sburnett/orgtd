package orgtd

import "testing"

func TestReadmeIsEmbedded(t *testing.T) {
	if Readme == "" {
		t.Fatal("Readme is empty — go:embed didn't pick up README.md")
	}
	const want = "# orgtd"
	if len(Readme) < len(want) || Readme[:len(want)] != want {
		t.Errorf("Readme = %q..., want it to start with %q", Readme[:min(40, len(Readme))], want)
	}
}
