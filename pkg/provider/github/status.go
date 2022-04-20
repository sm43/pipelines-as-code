package github

import (
	"context"
	"fmt"
	"time"

	"github.com/google/go-github/v43/github"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/params/info"
	"github.com/openshift-pipelines/pipelines-as-code/pkg/provider"
)

const taskStatusTemplate = `
<table>
  <tr><th>Status</th><th>Duration</th><th>Name</th></tr>

{{- range $taskrun := .TaskRunList }}
<tr>
<td>{{ formatCondition $taskrun.Status.Conditions }}</td>
<td>{{ formatDuration $taskrun.Status.StartTime $taskrun.Status.CompletionTime }}</td><td>

{{ $taskrun.ConsoleLogURL }}

</td></tr>
{{- end }}
</table>`

func getCheckName(status provider.StatusOpts, pacopts *info.PacOpts) string {
	if pacopts.ApplicationName != "" {
		if status.OriginalPipelineRunName == "" {
			return pacopts.ApplicationName
		}
		return fmt.Sprintf("%s / %s", pacopts.ApplicationName, status.OriginalPipelineRunName)
	}
	return status.OriginalPipelineRunName
}

func (v *Provider) getExistingCheckRunID(ctx context.Context, runevent *info.Event) (*int64, error) {
	res, _, err := v.Client.Checks.ListCheckRunsForRef(ctx, runevent.Organization, runevent.Repository,
		runevent.SHA, &github.ListCheckRunsOptions{
			AppID: v.ApplicationID,
		})
	if err != nil {
		return nil, err
	}
	if *res.Total > 0 {
		// Their should be only one, since we CRUD it.. maybe one day we
		// will offer the ability to do multiple pipelineruns per commit then
		// things will get more complicated here.
		return res.CheckRuns[0].ID, nil
	}
	return nil, nil
}

func (v *Provider) createCheckRunStatus(ctx context.Context, runevent *info.Event, pacopts *info.PacOpts, status provider.StatusOpts) (*int64, error) {
	now := github.Timestamp{Time: time.Now()}
	checkrunoption := github.CreateCheckRunOptions{
		Name:       getCheckName(status, pacopts),
		HeadSHA:    runevent.SHA,
		Status:     github.String("in_progress"),
		DetailsURL: github.String(pacopts.LogURL),
		StartedAt:  &now,
	}

	checkRun, _, err := v.Client.Checks.CreateCheckRun(ctx, runevent.Organization, runevent.Repository, checkrunoption)
	if err != nil {
		return nil, err
	}
	return checkRun.ID, nil
}

// getOrUpdateCheckRunStatus create a status via the checkRun API, which is only
// available with Github apps tokens.
func (v *Provider) getOrUpdateCheckRunStatus(ctx context.Context, runevent *info.Event, pacopts *info.PacOpts, status provider.StatusOpts) error {
	var err error

	now := github.Timestamp{Time: time.Now()}
	if runevent.CheckRunID == nil {
		if runevent.CheckRunID, _ = v.getExistingCheckRunID(ctx, runevent); runevent.CheckRunID == nil {
			runevent.CheckRunID, err = v.createCheckRunStatus(ctx, runevent, pacopts, status)
			if err != nil {
				return err
			}
		}
	}

	checkRunOutput := &github.CheckRunOutput{
		Title:   &status.Title,
		Summary: &status.Summary,
		Text:    &status.Text,
	}

	opts := github.UpdateCheckRunOptions{
		Name:   getCheckName(status, pacopts),
		Status: &status.Status,
		Output: checkRunOutput,
	}

	if status.DetailsURL != "" {
		opts.DetailsURL = &status.DetailsURL
	}

	// Only set completed-at if conclusion is set (which means finished)
	if status.Conclusion != "" && status.Conclusion != "pending" {
		opts.CompletedAt = &now
		opts.Conclusion = &status.Conclusion
	}

	_, _, err = v.Client.Checks.UpdateCheckRun(ctx, runevent.Organization, runevent.Repository, *runevent.CheckRunID, opts)
	return err
}

// createStatusCommit use the classic/old statuses API which is available when we
// don't have a github app token
func (v *Provider) createStatusCommit(ctx context.Context, runevent *info.Event, pacopts *info.PacOpts, status provider.StatusOpts) error {
	var err error
	now := time.Now()
	switch status.Conclusion {
	case "skipped":
		status.Conclusion = "success" // We don't have a choice than setting as succes, no pending here.
	case "neutral":
		status.Conclusion = "success" // We don't have a choice than setting as succes, no pending here.
	}
	if status.Status == "in_progress" {
		status.Conclusion = "pending"
	}

	ghstatus := &github.RepoStatus{
		State:       github.String(status.Conclusion),
		TargetURL:   github.String(status.DetailsURL),
		Description: github.String(status.Title),
		Context:     github.String(getCheckName(status, pacopts)),
		CreatedAt:   &now,
	}

	if _, _, err := v.Client.Repositories.CreateStatus(ctx,
		runevent.Organization, runevent.Repository, runevent.SHA, ghstatus); err != nil {
		return err
	}
	if status.Status == "completed" && status.Text != "" && runevent.EventType == "pull_request" {
		//payloadevent, ok := runevent.Event.(*github.PullRequestEvent)
		//if !ok {
		//	return fmt.Errorf("could not parse event: %+v", payloadevent)
		//}
		//payloadevent.GetPullRequest().GetNumber()
		prNumber := runevent.PullRequestNumber
		_, _, err = v.Client.Issues.CreateComment(ctx, runevent.Organization, runevent.Repository,
			prNumber,
			&github.IssueComment{
				Body: github.String(fmt.Sprintf("%s<br>%s", status.Summary, status.Text)),
			},
		)
		if err != nil {
			return err
		}
	}

	return nil
}

func (v *Provider) CreateStatus(ctx context.Context, runevent *info.Event, pacopts *info.PacOpts, status provider.StatusOpts) error {
	if v.Client == nil {
		return fmt.Errorf("cannot set status on github no token or url set")
	}

	switch status.Conclusion {
	case "success":
		status.Title = "✅ Success"
		status.Summary = fmt.Sprintf("%s has <b>successfully</b> validated your commit.", pacopts.ApplicationName)
	case "failure":
		status.Title = "❌ Failed"
		status.Summary = fmt.Sprintf("%s has <b>failed</b>.", pacopts.ApplicationName)
	case "skipped":
		status.Title = "➖ Skipped"
		status.Summary = fmt.Sprintf("%s is skipping this commit.", pacopts.ApplicationName)
	case "neutral":
		status.Title = "❓ Unknown"
		status.Summary = fmt.Sprintf("%s doesn't know what happened with this commit.", pacopts.ApplicationName)
	}

	if status.Status == "in_progress" {
		status.Title = "CI has Started"
		status.Summary = fmt.Sprintf("%s is running.", pacopts.ApplicationName)
	}

	if runevent.Provider.InfoFromRepo {
		return v.createStatusCommit(ctx, runevent, pacopts, status)
	}
	return v.getOrUpdateCheckRunStatus(ctx, runevent, pacopts, status)
}
