package hongguo

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
)

// Reference outputs of nas-backend's danmaku.py, as hongguo-drama keeps
// them (scripts/fixtures/danmaku-sign-vectors.json).
func TestCommentSigningVectors(t *testing.T) {
	b, err := os.ReadFile("testdata/danmaku-sign-vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Vectors []struct {
			DeviceID  string `json:"device_id"`
			InstallID string `json:"install_id"`
			Nonce     string `json:"nonce_hex"`
			RawQuery  string `json:"raw_query"`
			Stamp     int64  `json:"stamp"`
			StampMs   int64  `json:"stamp_ms"`
			Argus     string `json:"x_argus"`
			Gorgon    string `json:"x_gorgon"`
			Ladon     string `json:"x_ladon"`
			Ticket    string `json:"x_ss_req_ticket"`
		} `json:"vectors"`
	}
	if err := json.Unmarshal(b, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Vectors) < 3 {
		t.Fatal("too few vectors")
	}
	for _, v := range fixture.Vectors {
		if q := commentQuery(v.StampMs, v.DeviceID, v.InstallID); q != v.RawQuery {
			t.Errorf("query\n got %s\nwant %s", q, v.RawQuery)
		}
		nonce, _ := hex.DecodeString(v.Nonce)
		h := commentHeaders(v.RawQuery, v.Stamp, nonce)
		for name, want := range map[string]string{"X-Gorgon": v.Gorgon, "X-Ladon": v.Ladon, "X-Argus": v.Argus, "X-SS-Req-Ticket": v.Ticket} {
			if h[name] != want {
				t.Errorf("stamp %d %s\n got %s\nwant %s", v.Stamp, name, h[name], want)
			}
		}
	}
}
