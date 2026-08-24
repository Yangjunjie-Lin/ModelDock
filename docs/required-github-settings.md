# Required GitHub repository settings

> **NOT RELEASE EVIDENCE.** Repository-setting and Environment Gates remain
> **BLOCKED** until GitHub API read-back proves these values on the protected
> repository.

## GitHub UI

1. Open **Settings → Branches → Add branch protection rule** for main.
2. Enable **Require a pull request before merging**, one approval, dismiss
   stale approvals, require Code Owner review, and require conversation
   resolution.
3. Enable **Require status checks to pass**, **Require branches to be up to
   date**, and select every context below.
4. Enable enforcement for administrators. Leave force pushes and branch
   deletion disabled.
5. Open **Settings → Environments** and create commercial-beta and
   marketplace-production. Add at least one accountable human reviewer to
   each and restrict deployment to protected branches/Tags.

Required contexts:

- Go Format
- Go Vet
- Go Test
- Frontend Admin
- Frontend Console
- Exact Money
- Migration 1–25
- Commercial Integration
- Marketplace Integration
- Evidence Schema
- Evidence Signature
- Runtime Attestation
- Release Negative Tests
- Docker Build
- Trivy
- Govulncheck
- Gosec
- Gitleaks
- SBOM
- Actionlint
- Same-Digest Verification

## Exact gh api procedure

Authenticate an owner token with repository Administration permission, then
run from this checkout. The first command records the human reviewer user ID.

~~~powershell
$repo = "Yangjunjie-Lin/ModelDock"
$reviewerId = gh api users/Yangjunjie-Lin --jq .id
$protection = @{
  required_status_checks = @{
    strict = $true
    contexts = @(
      "Go Format", "Go Vet", "Go Test", "Frontend Admin", "Frontend Console",
      "Exact Money", "Migration 1–25", "Commercial Integration",
      "Marketplace Integration", "Evidence Schema", "Evidence Signature",
      "Runtime Attestation", "Release Negative Tests", "Docker Build", "Trivy",
      "Govulncheck", "Gosec", "Gitleaks", "SBOM", "Actionlint",
      "Same-Digest Verification"
    )
  }
  enforce_admins = $true
  required_pull_request_reviews = @{
    dismiss_stale_reviews = $true
    require_code_owner_reviews = $true
    required_approving_review_count = 1
    require_last_push_approval = $true
  }
  restrictions = $null
  required_conversation_resolution = $true
  allow_force_pushes = $false
  allow_deletions = $false
  block_creations = $false
  required_linear_history = $true
} | ConvertTo-Json -Depth 8
$protection | gh api --method PUT "repos/$repo/branches/main/protection" --input -

foreach ($environment in @("commercial-beta","marketplace-production")) {
  @{
    wait_timer = 0
    prevent_self_review = $true
    reviewers = @(@{ type = "User"; id = [int64]$reviewerId })
    deployment_branch_policy = @{
      protected_branches = $true
      custom_branch_policies = $false
    }
  } | ConvertTo-Json -Depth 6 |
    gh api --method PUT "repos/$repo/environments/$environment" --input -
}
~~~

Verify instead of trusting the write response:

~~~powershell
gh api "repos/$repo/branches/main/protection"
gh api "repos/$repo/environments/commercial-beta"
gh api "repos/$repo/environments/marketplace-production"
~~~

If any command returns 403, 404, or a plan/organization policy error, do not
claim configuration succeeded. Keep branch_protection and
production_environment_reviewers BLOCKED and have a repository owner apply
the UI procedure.
