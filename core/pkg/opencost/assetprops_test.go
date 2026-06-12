package opencost

import "testing"

func TestParseProviderHetzner(t *testing.T) {
	tests := map[string]string{
		"hetzner":       HetznerProvider,
		"hcloud":        HetznerProvider,
		"hetzner-cloud": HetznerProvider,
	}

	for input, want := range tests {
		t.Run(input, func(t *testing.T) {
			if got := ParseProvider(input); got != want {
				t.Errorf("ParseProvider(%q) = %q, want %q", input, got, want)
			}
		})
	}
}
