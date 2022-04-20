package provider

import (
	"regexp"
)

const (
	ProviderGitHubApp       = "GitHubApp"
	ProviderGitHubWebhook   = "GitHubWebhook"
	ProviderBitbucketCloud  = "BitbucketCloud"
	ProviderBitbucketServer = "BitbucketServer"
	ProviderGitlab          = "Gitlab"
)

var (
	retestRegex   = regexp.MustCompile(`(?m)^/retest\s*$`)
	oktotestRegex = regexp.MustCompile(`(?m)^/ok-to-test\s*$`)
)

func Valid(value string, validValues []string) bool {
	for _, v := range validValues {
		if v == value {
			return true
		}
	}
	return false
}

func IsRetestComment(comment string) bool {
	return retestRegex.MatchString(comment)
}

func IsOkToTestComment(comment string) bool {
	return oktotestRegex.MatchString(comment)
}
