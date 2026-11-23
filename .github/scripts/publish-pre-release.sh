#!/usr/bin/env bash
set -euo pipefail

repo="repos/$GITHUB_REPOSITORY"
repo_url="$GITHUB_SERVER_URL/$GITHUB_REPOSITORY"
build_tag="dev-$GITHUB_SHA-$GITHUB_RUN_ID-$GITHUB_RUN_ATTEMPT"
download_url="$repo_url/releases/download/$build_tag"
# Historical workflow runs still manage the pre-release tag
index_tag="development-builds"

archives=(
  dist/jm-automation-linux-amd64.tar.gz
  dist/jm-automation-linux-arm64.tar.gz
  dist/jm-automation-windows-amd64.zip
  dist/jm-automation-darwin-amd64.tar.gz
  dist/jm-automation-darwin-arm64.tar.gz
)

require_current_main() {
  local current_sha
  current_sha=$(gh api "$repo/git/ref/heads/main" | jq -er '.object.sha | strings | select(test("^([0-9a-f]{40}|[0-9a-f]{64})$"))')
  if [[ "$current_sha" != "$GITHUB_SHA" ]]; then
    echo "Skipping pre-release for $GITHUB_SHA because main is now $current_sha."
    exit 0
  fi
}

find_index_id() {
  gh api graphql \
    -f owner="$GITHUB_REPOSITORY_OWNER" -f name="${GITHUB_REPOSITORY#*/}" -f tag="$index_tag" \
    -f query='
      query($owner: String!, $name: String!, $tag: String!) {
        repository(owner: $owner, name: $name) {
          release(tagName: $tag) { databaseId isDraft isPrerelease }
        }
      }
    ' | jq -er '
      .data.repository | objects | .release |
      if . == null then ""
      elif .isDraft == false and .isPrerelease == true then .databaseId | numbers
      else error("Expected a published pre-release")
      end
    '
}

verify_build() {
  local expected_draft=$1
  gh api "$repo/releases/$build_id" | jq -e \
    --arg tag "$build_tag" --arg sha "$GITHUB_SHA" --arg url "$download_url" \
    --argjson draft "$expected_draft" --argjson manifest "$manifest" '
      .tag_name == $tag and .draft == $draft and .prerelease == true and
      (if $draft then .target_commitish == $sha else true end) and
      ([.assets[] | {name, size, digest}] | sort_by(.name)) == $manifest and
      all(.assets[]; .state == "uploaded" and ($draft or .browser_download_url == ($url + "/" + .name)))
    ' >/dev/null || { echo "::error::Release $build_tag does not match the build archives."; exit 1; }
}

changelog=$(git log -10 --pretty=format:'- %s (%h)' --no-merges)
notes=$(cat <<EOF
## 开发版本

此版本由 main 自动构建，建议生产环境使用[稳定版本]($repo_url/releases/latest)。

- 版本号：\`$VERSION\`
- Commit：[\`$GITHUB_SHA\`]($repo_url/commit/$GITHUB_SHA)

### 最近更新

$changelog

### 使用方法

下载并解压对应平台的文件，然后参考[项目说明]($repo_url/blob/main/README.md)配置和使用。macOS 二进制需要 macOS 13 或以上版本。
EOF
)

manifest=$(
  set -e
  for archive in "${archives[@]}"; do
    if [[ ! -s "$archive" ]]; then
      echo "::error::Missing or empty release archive: $archive" >&2
      exit 1
    fi
    hash=$(sha256sum "$archive")
    size=$(stat -c %s "$archive")
    printf '%s\t%s\tsha256:%s\n' "${archive##*/}" "$size" "${hash%% *}"
  done | jq -Rn '[inputs | split("\t") | {name: .[0], size: (.[1] | tonumber), digest: .[2]}] | sort_by(.name)'
)

require_current_main
gh release create "$build_tag" "${archives[@]}" --repo "$GITHUB_REPOSITORY" \
  --draft --prerelease --latest=false --target "$GITHUB_SHA" --title "$VERSION" --notes "$notes"
build_id=$(gh release view "$build_tag" --repo "$GITHUB_REPOSITORY" --json databaseId --jq '.databaseId')
verify_build true

require_current_main
gh api --method PATCH "$repo/releases/$build_id" --silent -F draft=false -f make_latest=false
gh api "$repo/git/ref/tags/$build_tag" | jq -e --arg sha "$GITHUB_SHA" \
  '.object.type == "commit" and .object.sha == $sha' >/dev/null ||
  { echo "::error::Published build tag does not point to $GITHUB_SHA."; exit 1; }
verify_build false

index_notes=$(cat <<EOF
## 最新开发版本

[查看构建详情和更新记录]($repo_url/releases/tag/$build_tag) · [查看提交]($repo_url/commit/$GITHUB_SHA)

- [Linux (x64)]($download_url/jm-automation-linux-amd64.tar.gz)
- [Linux (ARM64)]($download_url/jm-automation-linux-arm64.tar.gz)
- [Windows (x64)]($download_url/jm-automation-windows-amd64.zip)
- [macOS (Intel)]($download_url/jm-automation-darwin-amd64.tar.gz)
- [macOS (Apple Silicon)]($download_url/jm-automation-darwin-arm64.tar.gz)

macOS 二进制需要 macOS 13 或以上版本。生产环境建议使用[稳定版本]($repo_url/releases/latest)。
EOF
)
index_id=$(find_index_id)
require_current_main
if [[ -z "$index_id" ]]; then
  # GitHub resolves this branch target again when editing the release notes
  gh release create "$index_tag" --repo "$GITHUB_REPOSITORY" --prerelease --latest=false \
    --target main --title "Development Builds" --notes "$index_notes"
else
  gh api --method PATCH "$repo/releases/$index_id" --silent -f body="$index_notes"
fi
