package upstream

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/OXeu/certcenter/internal/certconfig"
)

type cloudStep struct {
	action   string
	response string
	check    func(*http.Request)
}

func mockCloudSteps(t *testing.T, steps []cloudStep) {
	t.Helper()
	previous := http.DefaultTransport
	index := 0
	t.Cleanup(func() {
		http.DefaultTransport = previous
		if index != len(steps) {
			t.Errorf("cloud calls: got %d, want %d", index, len(steps))
		}
	})
	http.DefaultTransport = fakeTransport(func(r *http.Request) (*http.Response, error) {
		action := r.Header.Get("X-TC-Action")
		if action == "" {
			for key, values := range r.Header {
				if strings.EqualFold(key, "x-acs-action") && len(values) > 0 {
					action = values[0]
				}
			}
		}
		if index >= len(steps) {
			t.Fatalf("unexpected cloud action %s", action)
		}
		step := steps[index]
		index++
		if action != step.action {
			t.Fatalf("cloud action %s, want %s", action, step.action)
		}
		if step.check != nil {
			step.check(r)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(step.response)), Request: r}, nil
	})
}

func advanceStage(t *testing.T, cfg certconfig.Upstream, oldID, newID string, p UpdateProgress, stage string) UpdateProgress {
	t.Helper()
	next, err := (Registry{}).Advance(t.Context(), cfg, oldID, newID, p)
	if err != nil || next.Stage != stage {
		t.Fatalf("advance %s: stage %s, want %s: %v", p.Stage, next.Stage, stage, err)
	}
	return next
}

func TestTencentReplacementWaitsForSuccessAndCheckedDeletion(t *testing.T) {
	mockCloudSteps(t, []cloudStep{
		{"CreateCertificateBindResourceSyncTask", `{"Response":{"CertTaskIds":[{"CertId":"old","TaskId":"scan"}]}}`, func(r *http.Request) {
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			if body["IsCache"] != float64(0) {
				t.Fatal("binding discovery used cached results")
			}
		}},
		{"DescribeCertificateBindResourceTaskResult", `{"Response":{"SyncTaskBindResourceResult":[{"TaskId":"scan","Status":0}]}}`, nil},
		{"DescribeCertificateBindResourceTaskResult", `{"Response":{"SyncTaskBindResourceResult":[{"TaskId":"scan","Status":1,"BindResourceResult":[{"ResourceType":"clb","BindResourceRegionResult":[{"Region":"ap-shanghai","TotalCount":2}]}]}]}}`, nil},
		{"DescribeHostUpdateRecord", `{"Response":{"TotalCount":0,"DeployRecordList":[]}}`, nil},
		{"UpdateCertificateInstance", `{"Response":{"DeployRecordId":0}}`, func(r *http.Request) {
			var body struct {
				OldCertificateId, CertificateId string
				ResourceTypes                   []string
				ResourceTypesRegions            []struct {
					ResourceType string
					Regions      []string
				}
			}
			json.NewDecoder(r.Body).Decode(&body)
			if body.OldCertificateId != "old" || body.CertificateId != "new" || len(body.ResourceTypes) != 1 || body.ResourceTypes[0] != "clb" || body.ResourceTypesRegions[0].Regions[0] != "ap-shanghai" {
				t.Fatal("incorrect update scope", body)
			}
		}},
		{"DescribeHostUpdateRecord", `{"Response":{"TotalCount":1,"DeployRecordList":[{"Id":77,"OldCertId":"old","CertId":"new"}]}}`, nil},
		{"DescribeHostUpdateRecordDetail", `{"Response":{"TotalCount":2,"SuccessTotalCount":1,"FailedTotalCount":0,"RunningTotalCount":1,"PendingTotalCount":0}}`, nil},
		{"DescribeHostUpdateRecordDetail", `{"Response":{"TotalCount":2,"SuccessTotalCount":2,"FailedTotalCount":0,"RunningTotalCount":0,"PendingTotalCount":0}}`, nil},
		{"DeleteCertificate", `{"Response":{"DeleteResult":false,"TaskId":"cleanup"}}`, func(r *http.Request) {
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			if body["CertificateId"] != "old" || body["IsCheckResource"] != true {
				t.Fatal("unsafe certificate deletion", body)
			}
		}},
		{"DescribeDeleteCertificatesTaskResult", `{"Response":{"DeleteTaskResult":[{"TaskId":"cleanup","CertId":"old","Status":0}]}}`, nil},
		{"DescribeDeleteCertificatesTaskResult", `{"Response":{"DeleteTaskResult":[{"TaskId":"cleanup","CertId":"old","Status":1}]}}`, nil},
	})
	cfg := certconfig.Upstream{Kind: "tencentcloud", AccessKey: "id", SecretKey: "secret", UpdateResources: true, DeleteOld: true}
	p := UpdateProgress{}
	for _, stage := range []string{"discovering", "discovering", "ready", "ready", "updating", "updating", "updated", "deleting", "deleting", "deleted"} {
		p = advanceStage(t, cfg, "old", "new", p, stage)
	}
	if !p.Complete(true) {
		t.Fatal("cleanup did not finish")
	}
}

func TestTencentFailuresRetainOldCertificate(t *testing.T) {
	for _, tc := range []struct{ name, stage, response, want string }{
		{"partial", "updating", `{"Response":{"TotalCount":2,"SuccessTotalCount":1,"FailedTotalCount":1,"RunningTotalCount":0,"PendingTotalCount":0}}`, "retry"},
		{"missing-counts", "updating", `{"Response":{"TotalCount":2,"SuccessTotalCount":2}}`, "updating"},
		{"empty", "updating", `{"Response":{"TotalCount":0,"SuccessTotalCount":0,"FailedTotalCount":0,"RunningTotalCount":0,"PendingTotalCount":0}}`, "updating"},
		{"bindings-remain", "deleting", `{"Response":{"DeleteTaskResult":[{"TaskId":"77","CertId":"old","Status":4}]}}`, "updated"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			action := "DescribeHostUpdateRecordDetail"
			if tc.stage == "deleting" {
				action = "DescribeDeleteCertificatesTaskResult"
			}
			mockCloudSteps(t, []cloudStep{{action, tc.response, nil}})
			p, err := (Registry{}).Advance(t.Context(), certconfig.Upstream{Kind: "tencentcloud", AccessKey: "id", SecretKey: "secret", UpdateResources: true, DeleteOld: true}, "old", "new", UpdateProgress{Stage: tc.stage, TaskID: "77"})
			if err == nil || p.Stage != tc.want {
				t.Fatal("unsafe update result", p, err)
			}
		})
	}
}

func TestAlibabaReplacementChecksAllWorkersAndBindings(t *testing.T) {
	resource1 := `{"Id":501,"CertId":11,"CloudName":"aliyun"}`
	resource2 := `{"Id":502,"CertId":11,"CloudName":"aliyun"}`
	mockCloudSteps(t, []cloudStep{
		{"ListCloudResources", `{"Total":2,"Data":[` + resource1 + `]}`, nil},
		{"ListCloudResources", `{"Total":2,"Data":[` + resource2 + `]}`, func(r *http.Request) {
			if r.URL.Query().Get("CurrentPage") != "2" {
				t.Fatal("missing pagination")
			}
		}},
		{"ListDeploymentJob", `{"Total":0,"Data":[]}`, nil},
		{"CreateDeploymentJob", `{"JobId":77}`, func(r *http.Request) {
			q := r.URL.Query()
			if q.Get("CertIds") != "22" || q.Get("ResourceIds") != "501,502" || q.Get("ContactIds") != "7,8" {
				t.Fatal("incorrect Alibaba update scope")
			}
		}},
		{"DescribeDeploymentJob", `{"Id":77,"Status":"editing"}`, nil},
		{"UpdateDeploymentJobStatus", `{"RequestId":"request"}`, func(r *http.Request) {
			if r.URL.Query().Get("Status") != "pending" {
				t.Fatal("job not started")
			}
		}},
		{"DescribeDeploymentJob", `{"Id":77,"Status":"processing"}`, nil},
		{"DescribeDeploymentJob", `{"Id":77,"Status":"success"}`, nil},
		{"ListWorkerResource", `{"Total":2,"Data":[{"Id":1,"CertId":22,"ResourceId":501,"Status":"success"}]}`, nil},
		{"ListWorkerResource", `{"Total":2,"Data":[{"Id":2,"CertId":22,"ResourceId":502,"Status":"success"}]}`, nil},
		{"ListCloudResources", `{"Total":0,"Data":[]}`, func(r *http.Request) {
			if r.URL.Query().Get("CloudName") != "" {
				t.Fatal("cleanup did not check other clouds")
			}
		}},
		{"ListCloudResources", `{"Total":2,"Data":[{"Id":501,"CertId":22,"CloudName":"aliyun"},{"Id":502,"CertId":22,"CloudName":"aliyun"}]}`, nil},
		{"DeleteUserCertificate", `{"RequestId":"deleted"}`, func(r *http.Request) {
			if r.URL.Query().Get("CertId") != "11" || len(r.URL.Query().Get("ClientToken")) != 64 {
				t.Fatal("incorrect cleanup target")
			}
		}},
	})
	cfg := certconfig.Upstream{Kind: "aliyun", AccessKey: "id", SecretKey: "secret", UpdateResources: true, DeleteOld: true, ContactIDs: "7, 8"}
	p := UpdateProgress{}
	for _, stage := range []string{"ready", "starting", "updating", "updating", "updated", "deleted"} {
		p = advanceStage(t, cfg, "11", "22", p, stage)
	}
}

func TestAlibabaRetainsOldCertificateOnIncompleteReplacement(t *testing.T) {
	for _, tc := range []struct {
		name     string
		progress UpdateProgress
		steps    []cloudStep
	}{
		{"bound", UpdateProgress{Stage: "updated"}, []cloudStep{{"ListCloudResources", `{"Total":1,"Data":[{"Id":501,"CertId":11,"CloudName":"aliyun"}]}`, nil}}},
		{"missing-binding", UpdateProgress{Stage: "updated", Resources: []string{"501"}}, []cloudStep{{"ListCloudResources", `{"Total":0,"Data":[]}`, nil}, {"ListCloudResources", `{"Total":0,"Data":[]}`, nil}}},
		{"failed-worker", UpdateProgress{Stage: "updating", TaskID: "77", Resources: []string{"501"}}, []cloudStep{{"DescribeDeploymentJob", `{"Id":77,"Status":"success"}`, nil}, {"ListWorkerResource", `{"Total":1,"Data":[{"Id":1,"CertId":22,"ResourceId":501,"Status":"error"}]}`, nil}}},
		{"missing-count", UpdateProgress{}, []cloudStep{{"ListCloudResources", `{"Data":[]}`, nil}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mockCloudSteps(t, tc.steps)
			p, err := (Registry{}).Advance(t.Context(), certconfig.Upstream{Kind: "aliyun", AccessKey: "id", SecretKey: "secret", UpdateResources: true, DeleteOld: true}, "11", "22", tc.progress)
			if err == nil || p.Stage == "deleted" {
				t.Fatal("unsafe cleanup", p, err)
			}
		})
	}
}

func TestCloudReplacementGuardsAndUploadIdentity(t *testing.T) {
	mockCloudSteps(t, nil)
	cfg := certconfig.Upstream{Kind: "tencentcloud", AccessKey: "id", SecretKey: "secret"}
	before := Revision(cfg)
	legacy := `certconfig.Upstream{Kind:"tencentcloud", AccessKey:"id", SecretKey:"secret", Region:"", Address:"", User:"", PrivateKey:"", HostKey:"", CertificatePath:"", KeyPath:"", ReloadCommand:""}`
	if before != fmt.Sprintf("%x", sha256.Sum256([]byte(legacy))) {
		t.Fatal("existing upload receipts cannot be reused after migration")
	}
	cfg.UpdateResources, cfg.DeleteOld, cfg.ContactIDs = true, true, "7"
	if Revision(cfg) != before {
		t.Fatal("update preferences invalidated upload receipts")
	}
	if _, err := (Registry{}).Advance(t.Context(), cfg, "same", "same", UpdateProgress{}); err == nil {
		t.Fatal("current certificate can be deleted")
	}
	cfg.DeleteOld = false
	advanceStage(t, cfg, "old", "new", UpdateProgress{Stage: "updated"}, "updated")
	cfg.SecretKey = "rotated"
	if Revision(cfg) == before {
		t.Fatal("credential changes reused a different destination")
	}
}

func TestAlibabaRecoversCreatedJobBeforeStartingIt(t *testing.T) {
	var name string
	p := UpdateProgress{Stage: "ready", Resources: []string{"501"}}
	cfg := certconfig.Upstream{Kind: "aliyun", AccessKey: "id", SecretKey: "secret", UpdateResources: true, ContactIDs: "7"}
	mockCloudSteps(t, []cloudStep{
		{"ListDeploymentJob", `{"Total":0,"Data":[]}`, nil},
		{"CreateDeploymentJob", `{"JobId":77}`, func(r *http.Request) { name = r.URL.Query().Get("Name") }},
	})
	advanceStage(t, cfg, "11", "22", p, "starting")
	if name == "" {
		t.Fatal("missing recoverable job name")
	}
	t.Run("restart", func(t *testing.T) {
		mockCloudSteps(t, []cloudStep{{"ListDeploymentJob", fmt.Sprintf(`{"Total":1,"Data":[{"Id":77,"Name":%q}]}`, name), nil}})
		recovered := advanceStage(t, cfg, "11", "22", p, "starting")
		if recovered.TaskID != "77" {
			t.Fatal("lost created job")
		}
	})
}

func TestTencentRetryAndAlreadyDeletedRecovery(t *testing.T) {
	cfg := certconfig.Upstream{Kind: "tencentcloud", AccessKey: "id", SecretKey: "secret", UpdateResources: true, DeleteOld: true}
	mockCloudSteps(t, []cloudStep{
		{"UpdateCertificateRecordRetry", `{"Response":{"RequestId":"retry"}}`, func(r *http.Request) {
			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			if body["DeployRecordId"] != float64(77) {
				t.Fatal("retry created a new update")
			}
		}},
		{"DeleteCertificate", `{"Response":{"Error":{"Code":"FailedOperation.CertificateNotFound","Message":"missing"},"RequestId":"gone"}}`, nil},
	})
	advanceStage(t, cfg, "old", "new", UpdateProgress{Stage: "retry", TaskID: "77"}, "updating")
	advanceStage(t, cfg, "old", "new", UpdateProgress{Stage: "updated"}, "deleted")
}
