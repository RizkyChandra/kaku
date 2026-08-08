package config

import "testing"

func TestCheckOnVolume(t *testing.T) {
	for _, tc := range []struct {
		dbPath, mount string
		wantErr       bool
	}{
		{"/data/kaku.db", "/data", false},
		{"/data/sub/kaku.db", "/data/", false},
		{"./data/kaku.db", "/data", true}, // relative: ephemeral disk
		{"/data2/kaku.db", "/data", true}, // prefix, not a child
		{"/srv/kaku.db", "/data", true},   // elsewhere entirely
		{"./kaku.db", "", false},          // no volume attached: not our call
	} {
		if err := checkOnVolume(tc.dbPath, tc.mount); (err != nil) != tc.wantErr {
			t.Errorf("checkOnVolume(%q, %q) = %v, want error: %v", tc.dbPath, tc.mount, err, tc.wantErr)
		}
	}
}
