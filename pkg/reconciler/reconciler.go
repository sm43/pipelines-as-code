package reconciler

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"

	"github.com/openshift-pipelines/pipelines-as-code/pkg/apis/pipelinesascode"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/kubeinteraction"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/pipelineascode"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider/bitbucketcloud"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider/bitbucketserver"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider/github"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider/gitlab"
	"github.com/tektoncd/pipeline/pkg/apis/pipeline/v1beta1"
	v1beta12 "github.com/tektoncd/pipeline/pkg/client/informers/externalversions/pipeline/v1beta1"
	pipelinerunreconciler "github.com/tektoncd/pipeline/pkg/client/injection/reconciler/pipeline/v1beta1/pipelinerun"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"knative.dev/pkg/logging"
	pkgreconciler "knative.dev/pkg/reconciler"
)

type Reconciler struct {
	run         *params.Run
	pipelinerun v1beta12.PipelineRunInformer
	kinteract   *kubeinteraction.Interaction
}

var (
	_ pipelinerunreconciler.Interface = (*Reconciler)(nil)
)

func (c *Reconciler) ReconcileKind(ctx context.Context, pr *v1beta1.PipelineRun) pkgreconciler.Event {
	logger := logging.FromContext(ctx)

	logger.Info("Checking status for pipelineRun: ", pr.GetName())

	if pr.IsDone() {
		logger.Infof("pipelineRun %v is done !!!  ", pr.GetName())
		return c.reportStatus(ctx, pr)
	}

	logger.Infof("pipelineRun %v is not done ...  ", pr.GetName())
	return nil
}

func (c *Reconciler) reportStatus(ctx context.Context, pr *v1beta1.PipelineRun) error {
	logger := logging.FromContext(ctx)

	// fetch repository CR for pipelineRun
	prLabels := pr.GetLabels()
	repoName := prLabels[filepath.Join(pipelinesascode.GroupName, "repository")]

	repo, err := c.run.Clients.PipelineAsCode.PipelinesascodeV1alpha1().
		Repositories(pr.Namespace).Get(ctx, repoName, v1.GetOptions{})
	if err != nil {
		return err
	}

	prAnno := pr.GetAnnotations()
	keepMaxPipeline, ok := prAnno[filepath.Join(pipelinesascode.GroupName, "max-keep-runs")]
	if ok {
		max, err := strconv.Atoi(keepMaxPipeline)
		if err != nil {
			return err
		}

		err = c.kinteract.CleanupPipelines(ctx, repo, pr, max)
		if err != nil {
			return err
		}
	}

	event := info.NewEvent()

	providerType := prAnno[filepath.Join(pipelinesascode.GroupName, "provider")]
	var gitProvider provider.Interface
	switch providerType {
	case provider.ProviderGitHubApp:
		git := &github.Provider{}
		id, _ := strconv.Atoi(prAnno[filepath.Join(pipelinesascode.GroupName, "installation-id")])
		gheURL := prAnno[filepath.Join(pipelinesascode.GroupName, "ghe-url")]
		event.Provider.Token, err = git.GetAppToken(ctx, c.run.Clients.Kube, gheURL, int64(id))
		if err != nil {
			return err
		}
		gitProvider = git
	case provider.ProviderGitHubWebhook:
		gitProvider = &github.Provider{}
	case provider.ProviderGitlab:
		gitProvider = &gitlab.Provider{}
	case provider.ProviderBitbucketCloud:
		gitProvider = &bitbucketcloud.Provider{}
	case provider.ProviderBitbucketServer:
		gitProvider = &bitbucketserver.Provider{}
	default:
		logger.Error("failed to detect provider for pipelinerun: ", pr.GetName())
		return nil
	}

	// from labels
	event.Organization = prLabels[filepath.Join(pipelinesascode.GroupName, "url-org")]
	event.Repository = prLabels[filepath.Join(pipelinesascode.GroupName, "url-repository")]
	event.EventType = prLabels[filepath.Join(pipelinesascode.GroupName, "event-type")]
	event.BaseBranch = prLabels[filepath.Join(pipelinesascode.GroupName, "branch")]
	event.SHA = prLabels[filepath.Join(pipelinesascode.GroupName, "sha")]

	// from annotation
	event.SHATitle = prAnno[filepath.Join(pipelinesascode.GroupName, "sha-title")]
	event.SHAURL = prAnno[filepath.Join(pipelinesascode.GroupName, "sha-url")]

	prNumber := prAnno[filepath.Join(pipelinesascode.GroupName, "pull-request")]
	if prNumber != "" {
		event.PullRequestNumber, _ = strconv.Atoi(prNumber)
	}

	if repo.Spec.GitProvider != nil {
		if err := pipelineascode.SecretFromRepository(ctx, c.run, c.kinteract, gitProvider.GetConfig(), event, repo); err != nil {
			return err
		}
	} else {
		event.Provider.WebhookSecret, _ = pipelineascode.GetCurrentNSWebhookSecret(ctx, c.kinteract)
	}
	// remove the generated secret after completion of pipelinerun
	if c.run.Info.Pac.SecretAutoCreation {
		err = c.kinteract.DeleteBasicAuthSecret(ctx, event, repo.GetNamespace())
		if err != nil {
			return fmt.Errorf("deleting basic auth secret has failed: %w ", err)
		}
	}

	newPr, err := postFinalStatus(ctx, c.run, gitProvider, event, pr)
	if err != nil {
		return err
	}

	return updateRepoRunStatus(ctx, c.run, event, newPr, repo)
}
