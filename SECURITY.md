# Security policy

Please don't report vulnerabilities in public issues. Use
[GitHub private vulnerability reporting](https://github.com/TulipFarm/huntsman/security/advisories/new) instead.

Include the affected version or commit, steps to reproduce, and the impact. We aim to reply within 7 days.

Huntsman fetches arbitrary URLs, so issues such as server-side request forgery (reaching private addresses while
`HUNTSMAN_ALLOW_PRIVATE=false`), auth bypass when `HUNTSMAN_TOKEN` is set, and container escapes are in scope.
