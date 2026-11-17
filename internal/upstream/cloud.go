// Package upstream provides a uniform certificate deployment interface.
package upstream

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/OXeu/certcenter/internal/certconfig"
	cas "github.com/alibabacloud-go/cas-20200407/v4/client"
	openapi "github.com/alibabacloud-go/darabonba-openapi/v2/utils"
	"github.com/alibabacloud-go/tea/dara"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	tcerr "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/errors"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/profile"
	ssl "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/ssl/v20191205"
)

// Bundle carries validated PEM material to a deployment provider.
type Bundle struct {
	Name, Fingerprint       string
	Certificate, PrivateKey []byte
}

// Provider uploads or deploys one certificate and returns an external receipt.
type Provider interface {
	Upload(context.Context, certconfig.Upstream, Bundle) (string, error)
}

// Registry dispatches configured cloud and SSH upstreams.
type Registry struct{}

// Revision binds deployment receipts to the destination and its configuration.
func Revision(cfg certconfig.Upstream) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%#v", cfg))))
}

// Upload selects the configured upstream without sharing credentials between providers.
func (Registry) Upload(ctx context.Context, cfg certconfig.Upstream, bundle Bundle) (string, error) {
	switch cfg.Kind {
	case "tencentcloud":
		return uploadTencent(ctx, cfg, bundle)
	case "aliyun":
		return uploadAliyun(ctx, cfg, bundle)
	case "ssh":
		return uploadSSH(ctx, cfg, bundle)
	default:
		return "", errors.New("unsupported certificate upstream")
	}
}

func uploadTencent(ctx context.Context, cfg certconfig.Upstream, bundle Bundle) (string, error) {
	p := profile.NewClientProfile()
	p.HttpProfile.ReqTimeout = 60
	client, err := ssl.NewClient(common.NewCredential(cfg.AccessKey, cfg.SecretKey), cfg.Region, p)
	if err != nil {
		return "", errors.New("cannot initialize Tencent SSL client")
	}
	req := ssl.NewUploadCertificateRequest()
	req.CertificatePublicKey = common.StringPtr(string(bundle.Certificate))
	req.CertificatePrivateKey = common.StringPtr(string(bundle.PrivateKey))
	req.CertificateType = common.StringPtr("SVR")
	req.Alias = common.StringPtr(bundle.Name)
	req.Repeatable = common.BoolPtr(false)
	resp, err := client.UploadCertificateWithContext(ctx, req)
	if err != nil {
		var apiErr *tcerr.TencentCloudSDKError
		if errors.As(err, &apiErr) {
			return "", fmt.Errorf("tencent SSL upload: %s", apiErr.GetCode())
		}
		return "", errors.New("tencent SSL upload connection failed")
	}
	if resp == nil || resp.Response == nil {
		return "", errors.New("tencent SSL returned no receipt")
	}
	if id := resp.Response.CertificateId; id != nil && *id != "" {
		return *id, nil
	}
	if id := resp.Response.RepeatCertId; id != nil && *id != "" {
		return *id, nil
	}
	return "", errors.New("tencent SSL returned no certificate ID")
}

type httpDo func(*http.Request) (*http.Response, error)

// Call attaches operation cancellation to Alibaba SDK requests.
func (fn httpDo) Call(req *http.Request, _ *http.Transport) (*http.Response, error) { return fn(req) }

func uploadAliyun(ctx context.Context, cfg certconfig.Upstream, bundle Bundle) (string, error) {
	httpClient := &http.Client{Timeout: 60 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	region := cfg.Region
	if region == "" {
		region = "cn-hangzhou"
	}
	client, err := cas.NewClient(&openapi.Config{AccessKeyId: dara.String(cfg.AccessKey), AccessKeySecret: dara.String(cfg.SecretKey), RegionId: dara.String(region), Endpoint: dara.String("cas.aliyuncs.com"), HttpClient: httpDo(func(req *http.Request) (*http.Response, error) {
		resp, err := httpClient.Do(req.Clone(ctx))
		if err != nil {
			return nil, fmt.Errorf("alibaba SSL request: %w", err)
		}
		return resp, nil
	})})
	if err != nil {
		return "", errors.New("cannot initialize Alibaba SSL client")
	}
	resp, err := client.UploadUserCertificateWithOptions(&cas.UploadUserCertificateRequest{Name: dara.String(bundle.Name), Cert: dara.String(string(bundle.Certificate)), Key: dara.String(string(bundle.PrivateKey)), ClientToken: dara.String(bundle.Fingerprint)}, &dara.RuntimeOptions{})
	if err != nil {
		var apiErr dara.BaseError
		if errors.As(err, &apiErr) && apiErr.GetCode() != nil {
			return "", fmt.Errorf("alibaba SSL upload: %s", *apiErr.GetCode())
		}
		return "", errors.New("alibaba SSL upload failed; check RAM permissions and certificate quota")
	}
	if resp == nil || resp.Body == nil || resp.Body.CertId == nil || *resp.Body.CertId <= 0 {
		return "", errors.New("alibaba SSL returned no certificate ID")
	}
	return strconv.FormatInt(*resp.Body.CertId, 10), nil
}
