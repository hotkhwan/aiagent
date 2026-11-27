# GitHub Sponsors — setup notes for the owner

**Status: live for `hotkhwan`** (public page checked 2026-11-27:
<https://github.com/sponsors/hotkhwan>). `.github/FUNDING.yml` and the README's
"Support the project" section point there. Keep the profile text and tiers
below in step with what the dashboard shows.

GitHub Sponsors is optional support for open-source maintenance. It is
separate from the phone app's Pro subscription: it does not unlock Pro, the
push relay or any paid feature, and it is not an investment, a share or a
profit split.

## Owner-only steps

Only the account owner can do these; never hand payout, identity or tax
details to an agent.

1. Decide who receives the money: the personal account `hotkhwan`, or a GitHub
   **organization** for JOINANNY if one exists and is eligible. A GitLab group
   is not a GitHub organization. If the income should be the company's, choose
   the entity and bank account that match, and ask your accountant about tax.
2. Turn on two-factor authentication for that account.
3. Start at <https://github.com/sponsors> and follow
   [personal account setup](https://docs.github.com/en/sponsors/receiving-sponsorships-through-github-sponsors/setting-up-github-sponsors-for-your-personal-account)
   or [organization setup](https://docs.github.com/en/sponsors/receiving-sponsorships-through-github-sponsors/setting-up-github-sponsors-for-your-organization).
   Check eligibility for your region, payout method and fees there; they are
   GitHub's to state, not ours.
4. Fill in the profile (text below), create the tiers, submit, and wait for
   GitHub to approve it.
5. Open `https://github.com/sponsors/<account>` signed out. When it loads, do
   the two repository changes under **After activation**.

## Profile text

```text
We're JOINANNY, an independent studio building a future where everyone can have their own personal AI team.

AI Agent World is a self-hosted personal command center for AI agents, starting with Claude Code and Codex. Its server is open source.

Your sponsorship helps fund open-source maintenance, security updates, compatibility with evolving AI tools, documentation, and easier installation.

Sponsorship is optional and separate from the Mobile Pro subscription.

One person. Many agents. One world.

Thank you for supporting independent development.
```

## Tiers

Start with three; add more only when there is someone to look after them.

| Tier | Amount | Frequency | What it says |
|---|---|---|---|
| Buy us a coffee | $2 | One-time | Supports development |
| Project Backer | $5 | Monthly | Supports security and compatibility work |
| Founding Sponsor | $10 | Monthly | Optional public thanks, only with the sponsor's consent |

No tier promises features, an SLA, priority support or a team plan.

## After activation

1. Add `.github/FUNDING.yml` naming the account that was actually approved
   (one account, not both):

   ```yaml
   github: [hotkhwan]
   ```

2. Add this section to the README, above "More documentation":

   ```markdown
   ## Support the project

   If AI Agent World helps you, consider giving the repository a star,
   sharing feedback, or sponsoring open-source development.

   [Become a sponsor](https://github.com/sponsors/hotkhwan)

   Sponsorship is optional and does not unlock Mobile Pro features.
   ```

Never ask for stars or sponsorship in exchange for anything, and never buy
stars or trade them.
