# Issue Tracker

Issues and PRDs for this repo live in GitHub Issues.

Use the `gh` CLI from this repository checkout.

## Conventions

- Create an issue: `gh issue create --title "..." --body-file <file>`.
- Read an issue: `gh issue view <number> --comments --json number,title,body,comments,labels,url`.
- List issues: `gh issue list --state open --json number,title,body,labels,comments,url`.
- Comment on an issue: `gh issue comment <number> --body-file <file>`.
- Apply labels: `gh issue edit <number> --add-label "<label>"`.
- Remove labels: `gh issue edit <number> --remove-label "<label>"`.
- Close an issue: `gh issue close <number> --comment "..."`.

## Pull Requests

PRs as a request surface: no.

Collaborator PRs may still be reviewed when Bram asks, but `/triage` should not treat external PRs as feature requests by default.

## Skill Publishing

When a skill says "publish to the issue tracker", create a GitHub issue.

When a skill says "fetch the relevant ticket", run `gh issue view <number> --comments`.
