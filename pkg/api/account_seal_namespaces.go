package api

// Secretbox namespaces of integration credentials apid seals and opens, the
// account seal domain's values (ADR 20261022111812). They live here so the
// operator re-seal shares them with the packages that write the values.
const (
	// GitLabOAuthSecretNamespace binds an account's GitLab OAuth tokens.
	GitLabOAuthSecretNamespace = "gitlab-gitops-oauth"
	// GitLabOAuthSecretMaxBytes bounds the sealed token document.
	GitLabOAuthSecretMaxBytes = 16384
	// IssueSlackOAuthFlowNamespace binds a pending Slack consent flow.
	IssueSlackOAuthFlowNamespace = "issue-slack-oauth-flow-v1"
	// IssueTrackerLinearCredentialNamespace binds a Linear API key credential.
	IssueTrackerLinearCredentialNamespace = "issue_tracker_linear_api_key_v1"
	// IssueTrackerLinearOAuthNamespace binds a Linear OAuth credential.
	IssueTrackerLinearOAuthNamespace = "issue_tracker_linear_oauth_v1"
	// IssueTrackerLinearOAuthFlowNamespace binds a pending Linear consent flow.
	IssueTrackerLinearOAuthFlowNamespace = "issue_tracker_linear_oauth_flow_v1"
)
