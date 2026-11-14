package update

import "testing"

func TestAvailableOnlyAuthorizesNewerSemanticVersions(t *testing.T) {
	previous := Version
	t.Cleanup(func() { Version = previous })
	for _, test := range []struct {
		installed string
		candidate string
		available bool
	}{
		{"0.14.0", "v0.13.0", false},
		{"v0.14.0", "0.14.0", false},
		{"0.14.0", "v0.14.1", true},
		{"0.14.9", "v0.15.0", true},
		{"0.99.99", "v1.0.0", true},
		{"2.0.0", "v1.99.99", false},
		{"0.14.0", "0.14.0-rc.1", false},
		{"0.14.0-rc.1", "0.14.0", true},
		{"0.14.0-rc.2", "0.14.0-rc.10", true},
		{"0.14.0-rc.10", "0.14.0-rc.2", false},
		{"0.14.0-rc.2", "0.14.0-rc.beta", true},
		{"0.14.0-alpha", "0.14.0-alpha.1", true},
		{"0.14.0-alpha.1", "0.14.0-alpha", false},
		{"0.14.0+local.1", "v0.14.0+release.2", false},
		{"0.14.0+local.1", "v0.14.1+release.2", true},
		{"0.14.0-rc.1+local.1", "v0.14.0-rc.1+release.2", false},
		{"0.14.0", "18446744073709551616.0.0", true},
		{"0.14.0-18446744073709551615", "0.14.0-18446744073709551616", true},
		{"dev", "v0.14.0", false},
		{"0.14.0", "latest", false},
		{"0.14", "0.15.0", false},
		{"0.14.0", "0.15", false},
		{"0.14.0", "0.015.0", false},
		{"0.14.0", "0.15.0-rc.01", false},
		{"0.14.0", "0.15.0+build..1", false},
		{"0.14.0", "v0.15.0 trailing", false},
		{"0.14.0", "v0.15.0/asset", false},
	} {
		t.Run(test.installed+"_to_"+test.candidate, func(t *testing.T) {
			Version = test.installed
			if actual := Available(test.candidate); actual != test.available {
				t.Fatalf("Available(%q) with Version=%q = %v; want %v", test.candidate, Version, actual, test.available)
			}
		})
	}
}

func TestSemanticVersionPrereleasePrecedence(t *testing.T) {
	previous := Version
	t.Cleanup(func() { Version = previous })
	versions := []string{
		"1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0-beta",
		"1.0.0-beta.2", "1.0.0-beta.11", "1.0.0-rc.1", "1.0.0",
	}
	for installed, version := range versions {
		Version = version
		for candidate, tag := range versions {
			if actual := Available(tag); actual != (candidate > installed) {
				t.Fatalf("Available(%q) with Version=%q = %v", tag, Version, actual)
			}
		}
	}
}
