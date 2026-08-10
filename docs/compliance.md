# Compliance and acceptable use

RelayDock is an authorized credential control plane for official provider APIs.
It is not an account creation, consumer-session aggregation, promotion
automation, or policy-evasion product.

## Allowed use

- Import API/project/service credentials that your organization is authorized
  to use.
- Route authorized application traffic through configured model aliases.
- Apply client quotas, concurrency controls, health checks, and bounded
  operational failover.
- Measure usage and produce internal cost estimates.
- Manage credentials in bulk after those credentials were lawfully obtained
  outside RelayDock through the provider's official process.
- Use an ordinary corporate forward proxy for approved network connectivity,
  provided it is not used to evade provider limits or controls.

## Prohibited use and excluded capability

RelayDock must not be extended or configured to:

- create accounts automatically or in bulk;
- automate email verification, OTP retrieval, or temporary-email enrollment;
- solve, outsource, suppress, or bypass CAPTCHA/Turnstile challenges;
- manipulate browser fingerprints or pool consumer cookies/sessions;
- extract private web tokens or reverse engineer a consumer web application;
- rotate proxies, IP addresses, identities, projects, or credentials to evade a
  rate limit, abuse control, geographic restriction, suspension, or policy;
- automate trials, promotions, checkout links, referral value, new-user
  benefits, or other entitlements;
- conceal the origin, customer, project, or purpose of requests;
- export plaintext upstream secrets or expose them to downstream clients.

Claimed permission from a third party does not turn these excluded mechanisms
into RelayDock V1 features. A legitimate bulk credential import is limited to
already-issued official API credentials controlled by the administrator; it is
not a registration pipeline.

## Operator responsibilities

Operators are responsible for:

- complying with provider terms, applicable law, data-processing obligations,
  sanctions/export rules, and organizational policy;
- obtaining authorization for every imported credential and downstream user;
- selecting retention periods and a lawful basis for request metadata;
- configuring least-privilege project credentials and allowed model lists;
- monitoring `401`, `403`, `429`, abuse, and quota alerts without attempting to
  work around upstream enforcement;
- rotating and revoking secrets promptly after personnel or scope changes;
- clearly labeling RelayDock cost figures as internal estimates rather than
  provider invoices.

## Enforcement points

| Layer | Control |
| --- | --- |
| Product/API | No registration, CAPTCHA, trial, browser-session, or evasive proxy endpoints exist. |
| Credential model | Only provider API credential types are schedulable; password/account lists and cookies are invalid input. |
| Scheduler | Cooldown honors upstream limits; it does not select identities to defeat enforcement. |
| Network | No rotating proxy service is included in Compose. |
| Audit | Credential, route, API-key, user, and policy mutations are attributed to an actor. |
| Export | Credential exports omit secrets; request exports omit prompt content by default. |
| Documentation | Setup points administrators to official provider dashboards and APIs. |

## Incident response

1. Disable the affected RelayDock API key or provider credential.
2. Preserve redacted request IDs, audit records, timestamps, and relevant
   service logs.
3. Revoke the upstream credential in the provider's official dashboard.
4. Rotate RelayDock secrets if compromise scope is uncertain.
5. Notify the provider, customers, and authorities as required by contract or
   law.
6. Correct the root cause before re-enabling traffic; do not route around an
   upstream suspension.

Security reports should include the RelayDock version, deployment topology,
redacted request IDs, and reproduction steps that contain no live credentials
or personal data.

