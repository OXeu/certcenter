package upstream

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/OXeu/certcenter/internal/certconfig"
	cas "github.com/alibabacloud-go/cas-20200407/v4/client"
	"github.com/alibabacloud-go/tea/dara"
)

func advanceAliyun(ctx context.Context, cfg certconfig.Upstream, oldID, newID string, p UpdateProgress) (UpdateProgress, error) {
	oldCert, oldErr := strconv.ParseInt(oldID, 10, 64)
	newCert, newErr := strconv.ParseInt(newID, 10, 64)
	if oldErr != nil || newErr != nil || oldCert <= 0 || newCert <= 0 {
		return p, errors.New("invalid Alibaba certificate IDs")
	}
	client, err := aliyunClient(ctx, cfg)
	if err != nil {
		return p, err
	}
	switch p.Stage {
	case "":
		resources, err := aliyunResources(client, oldCert, true)
		if err != nil {
			return p, err
		}
		p.Resources, p.Stage = resources, "ready"
		if len(resources) == 0 {
			p.Stage = "updated"
		}
		return p, nil
	case "ready":
		return aliyunStartUpdate(client, cfg, oldID, newID, newCert, p)
	case "starting", "updating", "retry":
		return aliyunUpdateStatus(client, newCert, p)
	case "updated":
		remaining, err := aliyunResources(client, oldCert, false)
		if err != nil {
			return p, err
		}
		if len(remaining) != 0 {
			return p, errors.New("old Alibaba certificate still has cloud resource bindings; cleanup deferred")
		}
		current, err := aliyunResources(client, newCert, true)
		if err != nil {
			return p, err
		}
		bound := map[string]bool{}
		for _, id := range current {
			bound[id] = true
		}
		for _, id := range p.Resources {
			if !bound[id] {
				return p, errors.New("alibaba has not confirmed the replacement binding for every resource; old certificate retained")
			}
		}
		resp, err := client.DeleteUserCertificateWithOptions(&cas.DeleteUserCertificateRequest{CertId: &oldCert, ClientToken: dara.String(fmt.Sprintf("%x", sha256.Sum256([]byte("delete:"+oldID))))}, &dara.RuntimeOptions{})
		if err != nil {
			return p, cloudFailure("delete previous Alibaba certificate", err)
		}
		if resp == nil || resp.Body == nil || value(resp.Body.RequestId) == "" {
			return p, errors.New("alibaba returned no certificate cleanup receipt")
		}
		p.Stage = "deleted"
		return p, nil
	default:
		return p, errors.New("unknown Alibaba resource update checkpoint")
	}
}

func aliyunResources(client *cas.Client, certID int64, native bool) ([]string, error) {
	resources := []string{}
	seen := map[string]bool{}
	for page := int32(1); ; page++ {
		req := &cas.ListCloudResourcesRequest{CertIds: []*int64{&certID}, CurrentPage: &page, ShowSize: dara.Int32(50)}
		if native {
			req.CloudName = dara.String("aliyun")
		}
		resp, err := client.ListCloudResourcesWithOptions(req, &dara.RuntimeOptions{})
		if err != nil {
			return nil, cloudFailure("inspect Alibaba certificate bindings", err)
		}
		if resp == nil || resp.Body == nil || resp.Body.Total == nil || *resp.Body.Total < 0 {
			return nil, errors.New("alibaba returned no resource binding count")
		}
		for _, resource := range resp.Body.Data {
			if resource == nil || value(resource.Id) <= 0 || value(resource.CertId) != certID || (native && value(resource.CloudName) != "aliyun") {
				return nil, errors.New("alibaba resource bindings do not match the requested certificate")
			}
			id := strconv.FormatInt(*resource.Id, 10)
			if seen[id] {
				return nil, errors.New("alibaba returned duplicate resource pages")
			}
			seen[id] = true
			resources = append(resources, id)
		}
		if int64(len(resources)) == *resp.Body.Total {
			sort.Strings(resources)
			return resources, nil
		}
		if len(resp.Body.Data) == 0 || int64(len(resources)) > *resp.Body.Total {
			return nil, errors.New("incomplete Alibaba resource binding list")
		}
	}
}

func aliyunStartUpdate(client *cas.Client, cfg certconfig.Upstream, oldID, newID string, newCert int64, p UpdateProgress) (UpdateProgress, error) {
	if len(p.Resources) == 0 || strings.TrimSpace(cfg.ContactIDs) == "" {
		return p, errors.New("alibaba resource updates require resources and contact IDs")
	}
	name := fmt.Sprintf("certcenter-%x", sha256.Sum256([]byte(oldID+":"+newID+":"+strings.Join(p.Resources, ","))))[:50]
	count := int64(0)
	for page := int32(1); ; page++ {
		resp, err := client.ListDeploymentJobWithOptions(&cas.ListDeploymentJobRequest{CertId: &newCert, JobType: dara.String("user"), CurrentPage: &page, ShowSize: dara.Int32(50)}, &dara.RuntimeOptions{})
		if err != nil {
			return p, cloudFailure("recover Alibaba resource update", err)
		}
		if resp == nil || resp.Body == nil || resp.Body.Total == nil {
			return p, errors.New("alibaba returned no deployment job list")
		}
		for _, job := range resp.Body.Data {
			if job != nil && value(job.Name) == name && value(job.Id) > 0 {
				p.Stage, p.TaskID = "starting", strconv.FormatInt(*job.Id, 10)
				return p, nil
			}
		}
		count += int64(len(resp.Body.Data))
		if count >= *resp.Body.Total {
			break
		}
		if len(resp.Body.Data) == 0 {
			return p, errors.New("incomplete Alibaba deployment job list")
		}
	}
	contacts := strings.Split(cfg.ContactIDs, ",")
	for i := range contacts {
		contacts[i] = strings.TrimSpace(contacts[i])
	}
	resp, err := client.CreateDeploymentJobWithOptions(&cas.CreateDeploymentJobRequest{Name: &name, JobType: dara.String("user"), CertIds: &newID, ResourceIds: dara.String(strings.Join(p.Resources, ",")), ContactIds: dara.String(strings.Join(contacts, ","))}, &dara.RuntimeOptions{})
	if err != nil {
		return p, cloudFailure("create Alibaba resource update", err)
	}
	if resp == nil || resp.Body == nil || value(resp.Body.JobId) <= 0 {
		return p, errors.New("alibaba returned no deployment job ID")
	}
	p.Stage, p.TaskID = "starting", strconv.FormatInt(*resp.Body.JobId, 10)
	return p, nil
}

func aliyunUpdateStatus(client *cas.Client, newCert int64, p UpdateProgress) (UpdateProgress, error) {
	jobID, err := strconv.ParseInt(p.TaskID, 10, 64)
	if err != nil || jobID <= 0 {
		return p, errors.New("invalid Alibaba deployment job ID")
	}
	resp, err := client.DescribeDeploymentJobWithOptions(&cas.DescribeDeploymentJobRequest{JobId: &jobID}, &dara.RuntimeOptions{})
	if err != nil {
		return p, cloudFailure("read Alibaba resource update", err)
	}
	if resp == nil || resp.Body == nil || value(resp.Body.Id) != jobID || value(resp.Body.Del) != 0 || value(resp.Body.Rollback) != 0 {
		return p, errors.New("alibaba deployment job is missing, deleted or rolled back")
	}
	switch value(resp.Body.Status) {
	case "editing":
		if p.Stage != "starting" {
			return p, errors.New("alibaba deployment job returned to editing; old certificate retained")
		}
		if _, err := client.UpdateDeploymentJobStatusWithOptions(&cas.UpdateDeploymentJobStatusRequest{JobId: &jobID, Status: dara.String("pending")}, &dara.RuntimeOptions{}); err != nil {
			return p, cloudFailure("start Alibaba resource update", err)
		}
		p.Stage = "updating"
		return p, nil
	case "pending", "scheduling", "processing":
		p.Stage = "updating"
		return p, nil
	case "error":
		if p.Stage != "retry" {
			p.Stage = "retry"
			return p, errors.New("alibaba resource update failed; old certificate retained")
		}
		if err := aliyunWorkers(client, jobID, newCert, p.Resources, true); err != nil {
			return p, err
		}
		if _, err := client.UpdateDeploymentJobStatusWithOptions(&cas.UpdateDeploymentJobStatusRequest{JobId: &jobID, Status: dara.String("pending")}, &dara.RuntimeOptions{}); err != nil {
			return p, cloudFailure("restart Alibaba resource update", err)
		}
		p.Stage = "updating"
		return p, nil
	case "success":
		if err := aliyunWorkers(client, jobID, newCert, p.Resources, false); err != nil {
			return p, err
		}
		p.Stage = "updated"
		return p, nil
	default:
		return p, errors.New("unknown Alibaba deployment status; old certificate retained")
	}
}

func aliyunWorkers(client *cas.Client, jobID, newCert int64, resources []string, retry bool) error {
	expected := map[string]bool{}
	for _, id := range resources {
		expected[id] = false
	}
	seen := int64(0)
	for page := int32(1); ; page++ {
		resp, err := client.ListWorkerResourceWithOptions(&cas.ListWorkerResourceRequest{JobId: &jobID, CurrentPage: &page, ShowSize: dara.Int32(50)}, &dara.RuntimeOptions{})
		if err != nil {
			return cloudFailure("verify Alibaba resource workers", err)
		}
		if resp == nil || resp.Body == nil || resp.Body.Total == nil || *resp.Body.Total != int64(len(expected)) {
			return errors.New("alibaba worker count differs from discovered resources")
		}
		for _, worker := range resp.Body.Data {
			if worker == nil || value(worker.CertId) != newCert || value(worker.Id) <= 0 || value(worker.ResourceId) <= 0 {
				return errors.New("invalid Alibaba resource worker")
			}
			id := strconv.FormatInt(*worker.ResourceId, 10)
			if done, ok := expected[id]; !ok || done {
				return errors.New("alibaba worker resources differ from the requested replacement")
			}
			expected[id] = true
			switch value(worker.Status) {
			case "success":
			case "error":
				if !retry {
					return errors.New("alibaba reported a failed resource worker; old certificate retained")
				}
				if _, err := client.UpdateWorkerResourceStatusWithOptions(&cas.UpdateWorkerResourceStatusRequest{JobId: &jobID, WorkerId: worker.Id, Status: dara.String("pending")}, &dara.RuntimeOptions{}); err != nil {
					return cloudFailure("retry Alibaba resource worker", err)
				}
			case "pending", "scheduling", "processing":
				if !retry {
					return errors.New("alibaba resource workers are still pending; old certificate retained")
				}
			default:
				return errors.New("alibaba resource worker was rolled back or has unknown status; old certificate retained")
			}
			seen++
		}
		if seen == *resp.Body.Total {
			return nil
		}
		if len(resp.Body.Data) == 0 {
			return errors.New("incomplete Alibaba resource worker list")
		}
	}
}
