# Specs

Project specs for this repo, one file per spec: `NNNN-slug.md` (`0001-ember-v1.md`), numbered in order.

- Front matter: `id`, `title`, `status`, `scope`, `target_repos`, `assets`, `size`, `idea_issue`, `created`.
- Sections: Problem, Goals, Non-goals, Design (with at least one diagram, light and dark), Rollout, Open questions,
  and an Acceptance contract: one row per observable behavior, each with a runnable check, written before any
  implementation.
- A spec arrives as its own PR. Nazar merging it is the approval; until then it is a draft.
- Implementation PRs never edit an approved acceptance contract. A row that is wrong or impossible is explained in
  the PR and stays failing; a change to it is a new spec PR.
