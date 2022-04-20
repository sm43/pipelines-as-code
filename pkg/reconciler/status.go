package reconciler

import (
	"context"
	"path/filepath"

	"github.com/google/go-github/v43/github"
	apipac "github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode"
	pacv1a1 "github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode/v1alpha1"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/formatting"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/sort"
	tektonv1beta1 "github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	maxPipelineRunStatusRun = 5
)

func updateRepoRunStatus(ctx context.Context, cs *params.Run, event *info.Event, pr *tektonv1beta1.PipelineRun, repo *pacv1a1.Repository) error {
	refsanitized := formatting.SanitizeBranch(event.BaseBranch)
	repoStatus := pacv1a1.RepositoryRunStatus{
		Status:          pr.Status.Status,
		PipelineRunName: pr.Name,
		StartTime:       pr.Status.StartTime,
		CompletionTime:  pr.Status.CompletionTime,
		SHA:             &event.SHA,
		SHAURL:          &event.SHAURL,
		Title:           &event.SHATitle,
		LogURL:          github.String(cs.Clients.ConsoleUI.DetailURL(pr.GetNamespace(), pr.GetName())),
		EventType:       &event.EventType,
		TargetBranch:    &refsanitized,
	}

	// Get repo again in case it was updated while we were running the CI
	// NOTE: there may be a race issue we should maybe solve here, between the Get and
	// Update but we are talking sub-milliseconds issue here.
	lastrepo, err := cs.Clients.PipelineAsCode.PipelinesascodeV1alpha1().Repositories(
		pr.GetNamespace()).Get(ctx, repo.Name, metav1.GetOptions{})
	if err != nil {
		return err
	}

	// Append pipelinerun status files to the repo status
	if len(lastrepo.Status) >= maxPipelineRunStatusRun {
		copy(lastrepo.Status, lastrepo.Status[len(lastrepo.Status)-maxPipelineRunStatusRun+1:])
		lastrepo.Status = lastrepo.Status[:maxPipelineRunStatusRun-1]
	}

	lastrepo.Status = append(lastrepo.Status, repoStatus)
	nrepo, err := cs.Clients.PipelineAsCode.PipelinesascodeV1alpha1().Repositories(lastrepo.Namespace).Update(
		ctx, lastrepo, metav1.UpdateOptions{})
	if err != nil {
		return err
	}
	cs.Clients.Log.Infof("Repository status of %s has been updated", nrepo.Name)

	return nil
}

func postFinalStatus(ctx context.Context, cs *params.Run, providerintf provider.Interface, event *info.Event, createdPR *tektonv1beta1.PipelineRun) (*tektonv1beta1.PipelineRun, error) {
	pr, err := cs.Clients.Tekton.TektonV1beta1().PipelineRuns(createdPR.GetNamespace()).Get(
		ctx, createdPR.GetName(), metav1.GetOptions{},
	)
	if err != nil {
		return pr, err
	}

	taskStatus, err := sort.TaskStatusTmpl(pr, cs.Clients.ConsoleUI, providerintf.GetConfig().TaskStatusTMPL)
	if err != nil {
		return pr, err
	}

	status := provider.StatusOpts{
		Status:                  "completed",
		Conclusion:              formatting.PipelineRunStatus(pr),
		Text:                    taskStatus,
		PipelineRunName:         pr.Name,
		DetailsURL:              cs.Clients.ConsoleUI.DetailURL(pr.GetNamespace(), pr.GetName()),
		OriginalPipelineRunName: pr.GetLabels()[filepath.Join(apipac.GroupName, "original-prname")],
	}

	err = providerintf.CreateStatus(ctx, event, cs.Info.Pac, status)
	cs.Clients.Log.Infof("pipelinerun %s has %s", pr.Name, status.Conclusion)
	return pr, err
}
