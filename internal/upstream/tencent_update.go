package upstream

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"

	"github.com/OXeu/certcenter/internal/certconfig"
	"github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common"
	tcerr "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/common/errors"
	ssl "github.com/tencentcloud/tencentcloud-sdk-go/tencentcloud/ssl/v20191205"
)

func advanceTencent(ctx context.Context, cfg certconfig.Upstream, oldID, newID string, p UpdateProgress) (UpdateProgress, error) {
	client, err := tencentClient(cfg)
	if err != nil {
		return p, err
	}
	switch p.Stage {
	case "":
		req := ssl.NewCreateCertificateBindResourceSyncTaskRequest()
		req.CertificateIds, req.IsCache = []*string{&oldID}, common.Uint64Ptr(0)
		resp, err := client.CreateCertificateBindResourceSyncTaskWithContext(ctx, req)
		if err != nil {
			return p, cloudFailure("discover Tencent certificate bindings", err)
		}
		if resp != nil && resp.Response != nil {
			for _, task := range resp.Response.CertTaskIds {
				if task != nil && value(task.CertId) == oldID && value(task.TaskId) != "" {
					p.Stage, p.TaskID = "discovering", *task.TaskId
					return p, nil
				}
			}
		}
		return p, errors.New("tencent returned no certificate binding task")
	case "discovering":
		return tencentBindings(ctx, client, p)
	case "ready":
		return tencentStartUpdate(ctx, client, oldID, newID, p)
	case "updating":
		return tencentUpdateStatus(ctx, client, oldID, newID, p)
	case "retry":
		id, err := strconv.ParseInt(p.TaskID, 10, 64)
		if err != nil || id <= 0 {
			return p, errors.New("invalid Tencent update task ID")
		}
		req := ssl.NewUpdateCertificateRecordRetryRequest()
		req.DeployRecordId = &id
		if _, err := client.UpdateCertificateRecordRetryWithContext(ctx, req); err != nil {
			return p, cloudFailure("retry Tencent resource update", err)
		}
		p.Stage = "updating"
		return p, nil
	case "updated":
		req := ssl.NewDeleteCertificateRequest()
		req.CertificateId, req.IsCheckResource = &oldID, common.BoolPtr(true)
		resp, err := client.DeleteCertificateWithContext(ctx, req)
		if err != nil {
			var apiErr *tcerr.TencentCloudSDKError
			if errors.As(err, &apiErr) && apiErr.GetCode() == "FailedOperation.CertificateNotFound" {
				p.Stage = "deleted"
				return p, nil
			}
			return p, cloudFailure("delete previous Tencent certificate", err)
		}
		if resp == nil || resp.Response == nil {
			return p, errors.New("tencent returned no certificate deletion result")
		}
		if task := value(resp.Response.TaskId); task != "" {
			p.Stage, p.TaskID = "deleting", task
		} else if value(resp.Response.DeleteResult) {
			p.Stage = "deleted"
		} else {
			return p, errors.New("tencent did not confirm certificate deletion")
		}
		return p, nil
	case "deleting":
		return tencentDeleteStatus(ctx, client, oldID, p)
	default:
		return p, errors.New("unknown Tencent resource update checkpoint")
	}
}

func tencentBindings(ctx context.Context, client *ssl.Client, p UpdateProgress) (UpdateProgress, error) {
	req := ssl.NewDescribeCertificateBindResourceTaskResultRequest()
	req.TaskIds = []*string{&p.TaskID}
	resp, err := client.DescribeCertificateBindResourceTaskResultWithContext(ctx, req)
	if err != nil {
		return p, cloudFailure("read Tencent certificate bindings", err)
	}
	if resp == nil || resp.Response == nil {
		return p, errors.New("tencent returned no certificate binding status")
	}
	for _, result := range resp.Response.SyncTaskBindResourceResult {
		if result == nil || value(result.TaskId) != p.TaskID || result.Status == nil {
			continue
		}
		switch *result.Status {
		case 0:
			return p, nil
		case 1:
			if result.BindResourceResult == nil {
				return p, errors.New("missing Tencent resource binding results")
			}
			p.Types, p.Regions = nil, map[string][]string{}
			for _, resource := range result.BindResourceResult {
				if resource == nil || value(resource.ResourceType) == "" || len(resource.BindResourceRegionResult) == 0 {
					return p, errors.New("incomplete Tencent resource binding result")
				}
				kind := *resource.ResourceType
				found := false
				for _, region := range resource.BindResourceRegionResult {
					if region == nil || value(region.Error) != "" || region.TotalCount == nil {
						return p, errors.New("tencent could not inspect all certificate bindings")
					}
					if *region.TotalCount > 0 {
						found = true
						if name := value(region.Region); name != "" {
							p.Regions[kind] = append(p.Regions[kind], name)
						}
					}
				}
				if found {
					p.Types = append(p.Types, kind)
				}
			}
			p.Stage, p.TaskID = "ready", ""
			if len(p.Types) == 0 {
				p.Stage = "updated"
			}
			return p, nil
		default:
			return p, errors.New("tencent certificate binding discovery failed; old certificate retained")
		}
	}
	return p, errors.New("tencent returned no matching certificate binding task")
}

func tencentStartUpdate(ctx context.Context, client *ssl.Client, oldID, newID string, p UpdateProgress) (UpdateProgress, error) {
	for offset := uint64(0); ; offset += 100 {
		req := ssl.NewDescribeHostUpdateRecordRequest()
		req.CertificateId, req.OldCertificateId = &newID, &oldID
		req.Offset, req.Limit = &offset, common.Uint64Ptr(100)
		resp, err := client.DescribeHostUpdateRecordWithContext(ctx, req)
		if err != nil {
			return p, cloudFailure("recover Tencent resource update", err)
		}
		if resp == nil || resp.Response == nil || resp.Response.TotalCount == nil {
			return p, errors.New("tencent returned no update history")
		}
		for _, record := range resp.Response.DeployRecordList {
			if record != nil && value(record.CertId) == newID && value(record.OldCertId) == oldID && value(record.Id) > 0 {
				p.Stage, p.TaskID = "updating", strconv.FormatUint(*record.Id, 10)
				return p, nil
			}
		}
		if offset+uint64(len(resp.Response.DeployRecordList)) >= *resp.Response.TotalCount {
			break
		}
		if len(resp.Response.DeployRecordList) == 0 {
			return p, errors.New("incomplete Tencent update history")
		}
	}
	if len(p.Types) == 0 {
		return p, errors.New("tencent update has no discovered resource types")
	}
	req := ssl.NewUpdateCertificateInstanceRequest()
	req.OldCertificateId, req.CertificateId = &oldID, &newID
	req.ResourceTypes = common.StringPtrs(p.Types)
	keys := make([]string, 0, len(p.Regions))
	for kind := range p.Regions {
		keys = append(keys, kind)
	}
	sort.Strings(keys)
	for _, kind := range keys {
		req.ResourceTypesRegions = append(req.ResourceTypesRegions, &ssl.ResourceTypeRegions{ResourceType: common.StringPtr(kind), Regions: common.StringPtrs(p.Regions[kind])})
	}
	resp, err := client.UpdateCertificateInstanceWithContext(ctx, req)
	if err != nil {
		return p, cloudFailure("start Tencent resource update", err)
	}
	if resp == nil || resp.Response == nil || resp.Response.DeployRecordId == nil {
		return p, errors.New("tencent returned no update task status")
	}
	if *resp.Response.DeployRecordId > 0 {
		p.Stage, p.TaskID = "updating", strconv.FormatUint(*resp.Response.DeployRecordId, 10)
	}
	return p, nil
}

func tencentUpdateStatus(ctx context.Context, client *ssl.Client, oldID, newID string, p UpdateProgress) (UpdateProgress, error) {
	req := ssl.NewDescribeHostUpdateRecordDetailRequest()
	req.DeployRecordId, req.Limit = &p.TaskID, common.StringPtr("200")
	resp, err := client.DescribeHostUpdateRecordDetailWithContext(ctx, req)
	if err != nil {
		return p, cloudFailure("read Tencent resource update", err)
	}
	if resp == nil || resp.Response == nil {
		return p, errors.New("tencent returned no resource update status")
	}
	result := resp.Response
	if result.TotalCount == nil || *result.TotalCount <= 0 || result.SuccessTotalCount == nil || result.FailedTotalCount == nil || result.RunningTotalCount == nil || result.PendingTotalCount == nil {
		return p, errors.New("incomplete Tencent resource update counts")
	}
	if *result.SuccessTotalCount < 0 || *result.FailedTotalCount < 0 || *result.RunningTotalCount < 0 || *result.PendingTotalCount < 0 || *result.SuccessTotalCount > *result.TotalCount {
		return p, errors.New("invalid Tencent resource update counts")
	}
	detailsComplete := true
	for _, group := range result.RecordDetailList {
		if group == nil {
			return p, errors.New("invalid Tencent resource update details")
		}
		for _, record := range group.List {
			if record == nil || value(record.CertId) != newID || value(record.OldCertId) != oldID {
				return p, errors.New("tencent resource update certificate IDs do not match")
			}
			if record.Status == nil || *record.Status == 4 || *record.Status == 5 || *record.Status > 6 {
				return p, errors.New("tencent resource update was rolled back or has unknown status; old certificate retained")
			}
			if *record.Status == 2 {
				p.Stage = "retry"
				return p, errors.New("tencent resource update failed; old certificate retained")
			}
			if *record.Status == 0 || *record.Status == 3 {
				detailsComplete = false
			}
		}
	}
	if *result.FailedTotalCount > 0 {
		p.Stage = "retry"
		return p, errors.New("tencent resource update partially failed; old certificate retained")
	}
	if detailsComplete && *result.RunningTotalCount == 0 && *result.PendingTotalCount == 0 && *result.SuccessTotalCount == *result.TotalCount {
		p.Stage = "updated"
	}
	return p, nil
}

func tencentDeleteStatus(ctx context.Context, client *ssl.Client, oldID string, p UpdateProgress) (UpdateProgress, error) {
	req := ssl.NewDescribeDeleteCertificatesTaskResultRequest()
	req.TaskIds = []*string{&p.TaskID}
	resp, err := client.DescribeDeleteCertificatesTaskResultWithContext(ctx, req)
	if err != nil {
		return p, cloudFailure("read Tencent certificate cleanup", err)
	}
	if resp != nil && resp.Response != nil {
		for _, task := range resp.Response.DeleteTaskResult {
			if task == nil || value(task.TaskId) != p.TaskID || value(task.CertId) != oldID || task.Status == nil {
				continue
			}
			switch *task.Status {
			case 0:
				return p, nil
			case 1:
				p.Stage = "deleted"
				return p, nil
			default:
				p.Stage = "updated"
				return p, fmt.Errorf("tencent retained the old certificate; cleanup task status %d", *task.Status)
			}
		}
	}
	return p, errors.New("tencent returned no matching certificate cleanup result")
}
