package upstream

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/OXeu/certcenter/internal/certconfig"
)

type fakeTransport func(*http.Request) (*http.Response, error)

func (f fakeTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestCloudUploadRequestsAndReceipts(t *testing.T) {
	previous := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = previous })
	calls := 0
	http.DefaultTransport = fakeTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		var response string
		switch r.URL.Host {
		case "ssl.tencentcloudapi.com":
			if r.Header.Get("X-TC-Action") != "UploadCertificate" || r.Header.Get("Authorization") == "" {
				t.Error("missing Tencent operation or signature")
			}
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			if body["CertificatePublicKey"] != "certificate" || body["CertificatePrivateKey"] != "private-key" || body["Repeatable"] != false {
				t.Error("incorrect Tencent request")
			}
			response = `{"Response":{"CertificateId":"tencent-id","RequestId":"request"}}`
		case "cas.aliyuncs.com":
			r.Form = r.URL.Query()
			if r.Form.Get("Cert") != "certificate" || r.Form.Get("Key") != "private-key" || r.Form.Get("ClientToken") != "fingerprint" {
				t.Errorf("incorrect Alibaba request fields")
			}
			response = `{"CertId":12345,"RequestId":"request"}`
		default:
			t.Errorf("unexpected external request %s", r.URL.Host)
			response = `{}`
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(response)), Request: r}, nil
	})
	for _, kind := range []string{"tencentcloud", "aliyun"} {
		id, err := (Registry{}).Upload(t.Context(), certconfig.Upstream{Kind: kind, AccessKey: "id", SecretKey: "secret"}, Bundle{Name: "certcenter-test", Fingerprint: "fingerprint", Certificate: []byte("certificate"), PrivateKey: []byte("private-key")})
		if err != nil || id == "" {
			t.Fatalf("%s upload: %s %v", kind, id, err)
		}
	}
	if calls != 2 {
		t.Fatal("unexpected upload count", calls)
	}
}

func TestCancelledCloudUpload(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, kind := range []string{"tencentcloud", "aliyun"} {
		if _, err := (Registry{}).Upload(ctx, certconfig.Upstream{Kind: kind, AccessKey: "id", SecretKey: "secret"}, Bundle{}); err == nil {
			t.Fatal("cancelled upload succeeded", kind)
		}
	}
}
