//! Public GitHub branch metadata. No signing keys, private API, or credentials.
use anyhow::{bail, ensure, Context, Result};
use reqwest::{blocking::Client, header, StatusCode};
use serde::Deserialize;
use std::{io::Read, time::Duration};
use url::Url;

pub const REPOSITORY_URL: &str = "https://github.com/ddcat-ai/open-ai-canvas";
pub const DEFAULT_API_ROOT: &str = "https://api.github.com/repos/ddcat-ai/open-ai-canvas/";
const MAX_JSON_BYTES: u64 = 4 * 1024 * 1024;
/// Fixed tracked branch of the upstream repository.
pub const TRACKED_BRANCH: &str = "main";

/// Tip of the tracked branch. The upstream publishes no GitHub Releases.
#[derive(Debug, Clone)]
pub struct PublicRelease {
    pub version: String,
    pub commit: String,
    pub notes: String,
}

#[derive(Deserialize)]
struct CommitJson {
    sha: String,
    commit: Option<CommitDetail>,
}

#[derive(Deserialize)]
struct CommitDetail {
    message: Option<String>,
}

fn validate_api_root(api_root: &Url) -> Result<()> {
    ensure!(
        api_root.as_str() == DEFAULT_API_ROOT
            || [
                "https://ghproxy.net/",
                "https://ghfast.top/",
                "https://gh-proxy.com/"
            ]
            .iter()
            .any(|prefix| api_root.as_str() == format!("{prefix}{DEFAULT_API_ROOT}")),
        "源码来源仅支持影策官方 GitHub API 或已知 HTTPS 代理的官方仓库前缀。"
    );
    Ok(())
}

fn get_json<T: serde::de::DeserializeOwned>(client: &Client, url: &Url) -> Result<T> {
    let response = client
        .get(url.clone())
        .header(header::USER_AGENT, "yingce-launcher")
        .header(header::ACCEPT, "application/vnd.github+json")
        .timeout(Duration::from_secs(30))
        .send()
        .context("无法连接 GitHub 发布服务，请检查网络后重试。")?;
    match response.status() {
        StatusCode::NOT_FOUND => {
            bail!("GitHub 未找到上游仓库或默认分支。")
        }
        StatusCode::FORBIDDEN | StatusCode::TOO_MANY_REQUESTS => {
            bail!("GitHub 请求受限（可能超过匿名访问频率或访问被拒绝），请稍后重试。")
        }
        status if !status.is_success() => {
            bail!("GitHub 发布服务返回 HTTP {}，请稍后重试。", status.as_u16())
        }
        _ => {}
    }
    ensure!(response.url() == url, "GitHub API 返回了非预期重定向。");
    ensure!(
        response.content_length().unwrap_or(0) <= MAX_JSON_BYTES,
        "GitHub 发布信息过大，已停止读取。"
    );
    let mut bytes = Vec::new();
    response
        .take(MAX_JSON_BYTES + 1)
        .read_to_end(&mut bytes)
        .context("读取 GitHub 发布信息失败，请重试。")?;
    ensure!(
        bytes.len() as u64 <= MAX_JSON_BYTES,
        "GitHub 发布信息过大，已停止读取。"
    );
    serde_json::from_slice(&bytes).context("GitHub 发布信息格式无效。")
}

fn parse_commit(json: CommitJson) -> Result<PublicRelease> {
    ensure!(
        (json.sha.len() == 40 || json.sha.len() == 64)
            && json.sha.bytes().all(|b| b.is_ascii_hexdigit()),
        "GitHub 分支对应的提交编号无效。"
    );
    let commit = json.sha.to_ascii_lowercase();
    let notes = json
        .commit
        .and_then(|c| c.message)
        .unwrap_or_default()
        .chars()
        .take(2000)
        .collect();
    Ok(PublicRelease {
        version: format!("{TRACKED_BRANCH}-{}", &commit[..7]),
        commit,
        notes,
    })
}

/// Resolves the current full commit of the tracked branch.
pub fn fetch_latest(client: &Client, api_root: &Url) -> Result<PublicRelease> {
    validate_api_root(api_root)?;
    let mut commit_url = api_root.join("commits/")?;
    commit_url
        .path_segments_mut()
        .map_err(|_| anyhow::anyhow!("无法构造 GitHub 提交地址。"))?
        .pop_if_empty()
        .push(TRACKED_BRANCH);
    parse_commit(get_json(client, &commit_url)?)
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    fn parse(value: serde_json::Value) -> Result<PublicRelease> {
        parse_commit(serde_json::from_value(value)?)
    }

    #[test]
    fn resolves_branch_tip_to_full_commit_and_version() {
        let release = parse(json!({"sha": "AB".repeat(20), "commit": {"message": "fix: 示例

正文"}})).unwrap();
        assert_eq!(release.commit, "ab".repeat(20));
        assert_eq!(release.version, "main-abababa");
        assert!(release.notes.starts_with("fix: 示例"));
        assert!(parse(json!({"sha": "b".repeat(40)})).unwrap().notes.is_empty());
    }

    #[test]
    fn rejects_non_commit_sha() {
        for sha in ["main", "abc", &"z".repeat(40)] {
            assert!(parse(json!({"sha": sha})).is_err(), "{sha}");
        }
    }

    #[test]
    fn accepts_only_exact_official_api_or_known_proxy_prefix() {
        assert!(validate_api_root(&Url::parse(DEFAULT_API_ROOT).unwrap()).is_ok());
        for prefix in [
            "https://ghproxy.net/",
            "https://ghfast.top/",
            "https://gh-proxy.com/",
        ] {
            assert!(validate_api_root(
                &Url::parse(&format!("{prefix}{DEFAULT_API_ROOT}")).unwrap()
            )
            .is_ok());
        }
        for root in [
            "http://api.github.com/repos/ddcat-ai/open-ai-canvas/",
            "https://api.github.com/repos/other/open-ai-canvas/",
            "https://api.github.com/repos/ddcat-ai/open-ai-canvas/?x=1",
            "https://proxy.example/https://api.github.com/repos/ddcat-ai/open-ai-canvas/",
        ] {
            assert!(validate_api_root(&Url::parse(root).unwrap()).is_err());
        }
    }
}
