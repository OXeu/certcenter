package upstream

import (
	"context"
	"errors"
	"fmt"

	"github.com/OXeu/certcenter/internal/certconfig"
	"github.com/alibabacloud-go/tea/dara"
	tcerr "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/errors"
)

// UpdateProgress is a durable checkpoint containing only cloud resource and task IDs.
type UpdateProgress struct {
	Stage     string              `json:"stage"`
	TaskID    string              `json:"task_id,omitempty"`
	Types     []string            `json:"types,omitempty"`
	Regions   map[string][]string `json:"regions,omitempty"`
	Resources []string            `json:"resources,omitempty"`
}

// Complete includes optional cleanup in the deployment completion condition.
func (p UpdateProgress) Complete(cleanup bool) bool {
	return p.Stage == "deleted" || (p.Stage == "updated" && !cleanup)
}

// ResourceUpdater advances a persisted cloud resource replacement by one step.
type ResourceUpdater interface {
	Advance(context.Context, certconfig.Upstream, string, string, UpdateProgress) (UpdateProgress, error)
}

// Advance replaces resources bound to a previously uploaded certificate before cleanup.
func (Registry) Advance(ctx context.Context, cfg certconfig.Upstream, oldID, newID string, progress UpdateProgress) (UpdateProgress, error) {
	if oldID == "" || newID == "" || oldID == newID {
		return progress, errors.New("cloud replacement requires distinct old and new certificate IDs")
	}
	if !cfg.UpdateResources || progress.Complete(cfg.DeleteOld) {
		return progress, nil
	}
	switch cfg.Kind {
	case "tencentcloud":
		return advanceTencent(ctx, cfg, oldID, newID, progress)
	case "aliyun":
		return advanceAliyun(ctx, cfg, oldID, newID, progress)
	default:
		return progress, errors.New("cloud resource updates are unavailable for this upstream")
	}
}

func cloudFailure(operation string, err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%s: %w", operation, err)
	}
	var tencentError *tcerr.TencentCloudSDKError
	if errors.As(err, &tencentError) {
		return fmt.Errorf("%s: %s", operation, tencentError.GetCode())
	}
	var aliyunError dara.BaseError
	if errors.As(err, &aliyunError) && aliyunError.GetCode() != nil {
		return fmt.Errorf("%s: %s", operation, *aliyunError.GetCode())
	}
	return fmt.Errorf("%s failed; check cloud permissions and service availability", operation)
}

func value[T any](pointer *T) T {
	if pointer != nil {
		return *pointer
	}
	var zero T
	return zero
}
