#!/usr/bin/env bash
# `pnpm ship:token`: gives GitHub Actions the Cloudflare API token that
# .github/workflows/deploy-site.yml deploys with, so every push to main that
# touches site/ goes live without a local `pnpm ship`. Run it once, and again
# whenever the token is rolled or expires.
#
# You make the token in the Cloudflare dashboard (the script opens the page
# and says what to pick); the script never creates one. You paste it at a
# hidden prompt. It's never echoed or put on a command line: it goes to
# api.cloudflare.com to be checked, then to the repo's Actions secrets via
# `gh secret set`, which encrypts it before upload.
#
# Checks before anything is stored: Cloudflare accepts the token, it reaches
# the safina-site Worker, and it can read the Worker's safina.run custom
# domain. Then, if you say so, it also sets CLOUDFLARE_ACCOUNT_ID to the
# account that holds the Worker, so the two secrets can't disagree. Last, it
# offers one deploy run from the Actions workflow, which proves the token
# end to end. Needs gh (logged in, admin on the repo), curl and jq.
set -euo pipefail

# The one Worker and domain the site ships to, as in scripts/ship.mjs.
WORKER="safina-site"
DOMAIN="safina.run"
WORKFLOW="deploy-site.yml"
TOKENS_PAGE="https://dash.cloudflare.com/profile/api-tokens"

token=""

say() { printf '%s\n' "$*"; }
step() { printf '\n== %s\n' "$*"; }
die() {
	printf 'error: %s\n' "$*" >&2
	exit 1
}
# ask "Question?" Y|N: true for yes. The capital letter is the answer on Enter.
ask() {
	local reply hint="[y/N]"
	[[ $2 == Y ]] && hint="[Y/n]"
	read -r -p "$1 $hint " reply || reply=""
	case "${reply:-$2}" in [yY]*) return 0 ;; *) return 1 ;; esac
}
# cf PATH: a GET on the Cloudflare API with the pasted token. The header
# comes in on stdin, so the token never shows up in `ps`.
cf() {
	curl -sS --max-time 20 -H @- "https://api.cloudflare.com/client/v4$1" <<<"Authorization: Bearer $token"
}
cf_errors() { jq -r '[.errors[]?.message] | if length > 0 then join("; ") else "no reason given" end' <<<"$1"; }

for tool in gh curl jq; do
	command -v "$tool" >/dev/null || die "$tool isn't installed."
done
cd "$(dirname "$0")/.."

step "GitHub"
gh auth status >/dev/null 2>&1 || die "gh isn't logged in. Run: gh auth login"
repo=$(gh repo view --json nameWithOwner -q .nameWithOwner) || die "No GitHub repo for $(pwd)."
[[ $(gh api "repos/$repo" --jq .permissions.admin) == true ]] ||
	die "Your GitHub login isn't an admin of $repo, so it can't set Actions secrets."
secrets=$(gh secret list --repo "$repo" --json name -q '.[].name')
has() { grep -qx "$1" <<<"$secrets"; }
say "Repo: $repo"
for name in CLOUDFLARE_API_TOKEN CLOUDFLARE_ACCOUNT_ID; do
	if has "$name"; then say "  $name: set"; else say "  $name: missing"; fi
done

step "Make the token"
cat <<EOF
In the Cloudflare dashboard, on My Profile > API Tokens:

  1. Create Token, then "Edit Cloudflare Workers" > Use template.
  2. Name it: $WORKER deploy (GitHub Actions)
  3. Account Resources: Include > your account (not All accounts).
  4. Zone Resources: Include > Specific zone > $DOMAIN.
  5. Optional, least privilege: delete the Workers KV Storage and
     Workers R2 Storage rows; the site uses neither.
  6. Leave Client IP Address Filtering empty: GitHub's runners have no
     fixed address. An expiry date is fine; run this script again then.
  7. Continue to summary > Create Token, and copy it.

$TOKENS_PAGE
EOF
if [[ -t 0 ]]; then
	if command -v xdg-open >/dev/null; then
		xdg-open "$TOKENS_PAGE" >/dev/null 2>&1 &
	elif command -v open >/dev/null; then
		open "$TOKENS_PAGE"
	fi
fi

step "Paste it"
read -r -s -p "Token (hidden): " token || true
printf '\n'
token="${token//[[:space:]]/}"
[[ -n $token ]] || die "No token pasted."

step "Check it with Cloudflare"
verify=$(cf /user/tokens/verify) || die "Couldn't reach api.cloudflare.com."
if [[ $(jq -r '.result.status // empty' <<<"$verify") != active ]]; then
	die "Cloudflare doesn't accept it as an active user token ($(cf_errors "$verify")). Make it under My Profile > API Tokens, not Manage Account."
fi
say "Active."

# The template grants Account Settings Read, so the token can list its
# accounts; the one that holds the Worker is the one CI deploys to.
accounts=$(cf '/accounts?per_page=50') || die "Couldn't reach api.cloudflare.com."
[[ $(jq -r .success <<<"$accounts") == true ]] ||
	die "It can't list its accounts ($(cf_errors "$accounts")). Was the template's Account Settings Read removed?"
account=""
account_name=""
while IFS=$'\t' read -r id name; do
	[[ -n $id ]] || continue
	if [[ $(cf "/accounts/$id/workers/scripts/$WORKER/deployments" | jq -r .success) == true ]]; then
		account=$id
		account_name=$name
		break
	fi
done < <(jq -r '.result[]? | [.id, .name] | @tsv' <<<"$accounts")
[[ -n $account ]] ||
	die "It doesn't reach the $WORKER Worker. Under Account Resources, include the account that holds it."
say "Reaches $WORKER in account \"$account_name\"."

domains=$(cf "/accounts/$account/workers/domains?hostname=$DOMAIN") || die "Couldn't reach api.cloudflare.com."
if [[ $(jq -r .success <<<"$domains") != true ]]; then
	say "Warning: it can't read the Worker's custom domains ($(cf_errors "$domains"))."
	say "         The deploy may fail where wrangler checks $DOMAIN."
	ask "Store it anyway?" N || exit 1
elif [[ -z $(jq -r --arg w "$WORKER" '.result[]? | select(.service == $w) | .hostname' <<<"$domains") ]]; then
	say "Warning: $DOMAIN isn't attached to $WORKER as a custom domain right now."
	say "         A deploy will try to attach it (wrangler.jsonc routes it)."
	ask "Store it anyway?" N || exit 1
else
	say "Sees $DOMAIN on $WORKER."
fi

step "Store it in $repo"
if has CLOUDFLARE_API_TOKEN && ! ask "CLOUDFLARE_API_TOKEN is already set. Replace it?" Y; then
	say "Left as it was."
	exit 0
fi
printf '%s' "$token" | gh secret set CLOUDFLARE_API_TOKEN --repo "$repo"
say "CLOUDFLARE_API_TOKEN set."

question="Set CLOUDFLARE_ACCOUNT_ID to $account_name's ID, the account that holds $WORKER?"
has CLOUDFLARE_ACCOUNT_ID && question="Also set CLOUDFLARE_ACCOUNT_ID to $account_name's ID, the account that holds $WORKER, so both secrets match?"
if ask "$question" Y; then
	printf '%s' "$account" | gh secret set CLOUDFLARE_ACCOUNT_ID --repo "$repo"
	say "CLOUDFLARE_ACCOUNT_ID set."
elif ! has CLOUDFLARE_ACCOUNT_ID; then
	say "Without CLOUDFLARE_ACCOUNT_ID, the workflow still skips the deploy."
	exit 0
fi

step "Prove it"
head=$(gh api "repos/$repo/commits/main" --jq '.sha[:7] + " " + (.commit.message | split("\n")[0])')
say "One run of $WORKFLOW deploys main as it is now: $head"
say "If that's what safina.run serves already, nothing visible changes. ship.mjs"
say "smoke-tests it and rolls back if it fails."
if ! ask "Run it and watch?" Y; then
	say "Done. The next push to main that touches site/ deploys."
	exit 0
fi
before=$(gh run list --repo "$repo" --workflow "$WORKFLOW" --event workflow_dispatch --limit 1 --json databaseId -q '.[0].databaseId // empty')
gh workflow run "$WORKFLOW" --repo "$repo" --ref main
run=""
for _ in $(seq 30); do
	sleep 2
	run=$(gh run list --repo "$repo" --workflow "$WORKFLOW" --event workflow_dispatch --limit 1 --json databaseId -q '.[0].databaseId // empty')
	[[ -n $run && $run != "$before" ]] && break
	run=""
done
[[ -n $run ]] || die "The run didn't show up within a minute. See: gh run list --repo $repo --workflow $WORKFLOW"
if gh run watch "$run" --repo "$repo" --exit-status --interval 10; then
	say ""
	say "Deployed from Actions. Pushes to main that touch site/ now go live on their own."
else
	say ""
	die "The run failed. Its log: gh run view $run --repo $repo --log-failed"
fi
