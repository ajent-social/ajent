// SPDX-License-Identifier: Apache-2.0

package update

import "testing"

func TestNewer(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"0.7.1", "0.7.0", true},
		{"0.7.0", "0.7.0", false},
		{"v0.8.0", "0.7.9", true},
		{"0.10.0", "0.9.0", true},
		{"1.0.0", "1.0.0-rc.1", true},
		{"1.0.0-rc.1", "1.0.0", false},
		{"1.0.0-rc.2", "1.0.0-rc.10", false},
		{"1.0.0-rc.10", "1.0.0-rc.2", true},
		{"1.0.0-beta", "1.0.0-alpha", true},
		{"1.0.0-alpha.1", "1.0.0-alpha", true},
		{"1.0.0-1", "1.0.0-alpha", false},
		{"1.0.0+build.5", "1.0.0", false},
		{"dev", "0.7.0", false},
		{"0.7.0", "dev", false},
		{"01.0.0", "0.9.0", false},
		{"0.7.1-SNAPSHOT-abc1234", "0.7.0", true},
	}

	for _, tt := range tests {
		t.Run(tt.a+"_vs_"+tt.b, func(t *testing.T) {
			if got := Newer(tt.a, tt.b); got != tt.want {
				t.Errorf("Newer(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

func TestValid(t *testing.T) {
	tests := []struct {
		v    string
		want bool
	}{
		{"0.7.0", true},
		{"v0.7.0", true},
		{"1.0.0-rc.1", true},
		{"1.0.0+build.5", true},
		{"1.0.0-rc.1+build.5", true},
		{"dev", false},
		{"01.0.0", false},
		{"0.7", false},
		{"0.7.0.1", false},
		{"", false},
		{"0.7.0-", false},
		{"0.7.0-.", false},
		{"0.7.0-a..b", false},
	}

	for _, tt := range tests {
		t.Run(tt.v, func(t *testing.T) {
			if got := Valid(tt.v); got != tt.want {
				t.Errorf("Valid(%q) = %v, want %v", tt.v, got, tt.want)
			}
		})
	}
}
