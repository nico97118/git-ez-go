# Security and trust

This development project has no published supported-release lifecycle or designated
private security contact. Do not assume an LTS/security maintenance commitment.
If the future GitHub repository enables private vulnerability reporting, use its
Security → Report a vulnerability facility. Otherwise ask the maintainer for a
private reporting channel without posting exploit details or private data publicly.
A private contact/reporting policy remains a maintainer decision.

LLM-initiated shell commands run with the caller's privileges. They are **not
operating-system sandboxed**. Policy guards and independent proposal verification
are safety checks, not process isolation. Use trusted working copies, restrict
endpoint access appropriately and review proposed changes. Validation commands,
Git hooks, filters, signing programs and editors also execute with caller privileges.

Repository content and diagnostic output can be sent to the selected provider.
Credentials registered in configuration are filtered from retained evidence;
this is not a general secret-scanning guarantee for arbitrary repository content.
Use environment-based credentials and never commit tokens or personal audit runs.
Audit data is private local storage and can itself contain sensitive source code.
Offline audit consultation does not invoke a model.

The engine owns Git lifecycle, revision pins, checkout locks, staging, validation
and continuation. Mandatory proposal publication/integrity failures stop staging.
Loss of optional validation output emits an explicit warning while retaining the
actual check status. Cleanup uncertainty preserves recovery data and prevents
unsafe restoration. Use native Git recovery when instructed; no persistent agent
session is automatically resumed.

CI runs untrusted pull requests with read-only permissions and no provider/release
credentials. Publication requires an explicitly enabled repository setting and a
protected release environment; see the required [repository settings](docs/release-engineering.md).
Checksums alone authenticate neither a publisher nor a source revision.
