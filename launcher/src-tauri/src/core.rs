//! Independent source builds of the fixed public upstream repository.
use crate::{public_release::{self, PublicRelease, DEFAULT_API_ROOT, REPOSITORY_URL}, source_install};
use anyhow::{anyhow, bail, ensure, Context, Result};
use fs2::FileExt;
use reqwest::blocking::Client;
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::{
    collections::BTreeMap,
    fs::{self, File, OpenOptions},
    io::{Read, Write},
    net::TcpListener,
    path::{Component, Path, PathBuf},
    process::{Child, Command, Stdio},
    thread,
    time::{Duration, Instant, SystemTime, UNIX_EPOCH},
};
use url::Url;

#[cfg(test)]
const MAX_MANIFEST: u64 = 2 * 1024 * 1024;
const MAX_STATE: u64 = 64 * 1024 * 1024;
const MAX_ARCHIVE: u64 = 4 * 1024 * 1024 * 1024;
const MAX_EXPANDED: u64 = 12 * 1024 * 1024 * 1024;
const MAX_FILES: usize = 100_000;
const MAX_LOG: u64 = 8 * 1024 * 1024;

#[derive(Debug, Clone, Serialize, Deserialize, Default)]
#[serde(rename_all = "lowercase")]
pub enum SourceMode {
    #[default]
    Auto,
    Official,
    Mirror,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct Config {
    pub source_mode: SourceMode,
    pub official_url: Option<String>,
    pub mirror_url: Option<String>,
    pub port: u16,
    pub open_browser: bool,
}
impl Default for Config {
    fn default() -> Self {
        Self {
            source_mode: SourceMode::Mirror,
            official_url: Some(REPOSITORY_URL.into()),
            mirror_url: Some("https://gh-proxy.com/".into()),
            port: 37890,
            open_browser: true,
        }
    }
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct InstalledRelease {
    pub version: String,
    pub commit: String,
    pub sequence: u64,
    pub release_id: String,
    pub data_schema: u32,
    #[serde(default)]
    pub data_dir: Option<String>,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct Artifact {
    pub platform: String,
    pub arch: String,
    pub path: String,
    pub size: u64,
    pub sha256: String,
    pub kind: String,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct Migration {
    pub data_schema: u32,
    pub requires_migration: bool,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct ReleaseManifest {
    pub schema_version: u32,
    pub sequence: u64,
    pub expires_at: u64,
    pub version: String,
    pub commit: String,
    pub channel: String,
    pub min_launcher_version: String,
    pub release_id: String,
    pub artifacts: Vec<Artifact>,
    pub migration: Migration,
    pub release_notes: String,
}
#[derive(Debug, Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct UpdateInfo {
    pub manifest: ReleaseManifest,
    pub available: bool,
    pub source: String,
    pub can_install: bool,
    pub reason: Option<String>,
}
#[derive(Debug, Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct Status {
    pub config: Config,
    pub installed: Option<InstalledRelease>,
    pub running: bool,
    pub pid: Option<u32>,
    pub url: String,
    pub platform: String,
    pub repository_url: String,
    pub recovery_required: bool,
}
#[derive(Default, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
struct State {
    config: Config,
    installed: Option<InstalledRelease>,
    highest_sequence: u64,
    #[serde(default)]
    manifest_digest: Option<String>,
    #[serde(default)]
    files: BTreeMap<String, String>,
}
pub struct Manager {
    root: PathBuf,
    state: State,
    client: Client,
    child: Option<Child>,
    shutdown_token: Option<String>,
    pending_restore: Option<(Option<InstalledRelease>, BTreeMap<String, String>)>,
    _lock: File,
    #[cfg(windows)]
    job: Option<Job>,
}
impl Manager {
    pub fn new(root: PathBuf) -> Result<Self> {
        ensure!(
            root.is_absolute(),
            "Application data directory must be absolute"
        );
        fs::create_dir_all(&root)?;
        reject_link(&root)?;
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            fs::set_permissions(&root, fs::Permissions::from_mode(0o700))?;
        }
        for dir in ["state", "releases", "staging", "data", "logs", "cache", "data-versions", "sources", "temp", "runtime-home"] {
            let path = root.join(dir);
            fs::create_dir_all(&path)?;
            reject_link(&path)?;
        }
        let lock_path = root.join("state/manager.lock");
        if lock_path.exists() {
            reject_link(&lock_path)?;
        }
        let lock = OpenOptions::new()
            .create(true)
            .truncate(false)
            .read(true)
            .write(true)
            .open(root.join("state/manager.lock"))?;
        lock.try_lock_exclusive()
            .context("Another launcher owns this managed installation")?;
        let state_path = root.join("state/state.json");
        let state: State = if state_path.exists() {
            reject_link(&state_path)?;
            let bytes = read_bounded(File::open(&state_path)?, MAX_STATE)?;
            serde_json::from_slice(&bytes)
                .context("Invalid persisted state; manual recovery required")?
        } else {
            State::default()
        };
        validate_config(&state.config)?;
        if let Some(i) = &state.installed {
            validate_id(&i.release_id)?;
        }
        let client = Client::builder()
            .https_only(true)
            .redirect(reqwest::redirect::Policy::custom(|attempt| {
                if attempt.previous().len() >= 5 {
                    return attempt.error("Too many download redirects");
                }
                let url = attempt.url();
                let first_host = attempt
                    .previous()
                    .first()
                    .and_then(|u| u.host_str())
                    .unwrap_or("");
                let proxies = ["ghproxy.net", "ghfast.top", "gh-proxy.com"];
                let allowed = if proxies.contains(&first_host) {
                    url.host_str() == Some(first_host)
                } else {
                    [
                        "api.github.com",
                        "github.com",
                        "release-assets.githubusercontent.com",
                        "objects.githubusercontent.com",
                        "codeload.github.com",
                    ]
                    .contains(&url.host_str().unwrap_or(""))
                };
                if url.scheme() == "https"
                    && allowed
                    && url.username().is_empty()
                    && url.password().is_none()
                {
                    attempt.follow()
                } else {
                    attempt.error("Redirect outside approved public release hosts")
                }
            }))
            .user_agent("yingce-launcher/0.1.0")
            .connect_timeout(Duration::from_secs(10))
            .timeout(Duration::from_secs(600))
            .build()?;
        Ok(Self {
            root,
            state,
            client,
            child: None,
            shutdown_token: None,
            pending_restore: None,
            _lock: lock,
            #[cfg(windows)]
            job: None,
        })
    }
    pub fn status(&mut self) -> Result<Status> {
        if let Some(child) = self.child.as_mut() {
            if child.try_wait()?.is_some() {
                self.child = None;
                #[cfg(windows)]
                {
                    self.job = None;
                }
            }
        }
        Ok(Status {
            config: self.state.config.clone(),
            installed: self.state.installed.clone(),
            running: self.child.is_some(),
            pid: self.child.as_ref().map(Child::id),
            url: format!("http://127.0.0.1:{}", self.state.config.port),
            platform: format!("{}-{}", platform()?.0, platform()?.1),
            repository_url: REPOSITORY_URL.into(),
            recovery_required: self.pending_restore.is_some(),
        })
    }
    pub fn configure(&mut self, config: Config) -> Result<Status> {
        validate_config(&config)?;
        ensure!(
            self.child.is_none(),
            "Stop the managed runtime before changing configuration"
        );
        let old = self.state.config.clone();
        self.state.config = config;
        if let Err(e) = self.save() {
            self.state.config = old;
            return Err(e);
        }
        self.status()
    }
    pub fn check_updates(&mut self) -> Result<UpdateInfo> {
        let (manifest, source, _) = self.fetch_manifest()?;
        let available = self
            .state
            .installed
            .as_ref()
            .map_or(true, |i| manifest.commit != i.commit);
        let reason = public_install_reason(&manifest, self.state.installed.is_some());
        Ok(UpdateInfo {
            manifest,
            available,
            source,
            can_install: reason.is_none(),
            reason,
        })
    }
    fn fetch_manifest(&mut self) -> Result<(ReleaseManifest, String, Vec<u8>)> {
        let mut errors = Vec::new();
        for source in public_sources(&self.state.config, DEFAULT_API_ROOT)? {
            match public_release::fetch_latest(&self.client, &source) {
                Ok(release) => {
                    let manifest = manifest_from_public(&release)?;
                    let raw = serde_json::to_vec(&manifest)?;
                    let origin = format!("{REPOSITORY_URL}/tree/{}", public_release::TRACKED_BRANCH);
                    return Ok((manifest, origin, raw));
                }
                Err(error) => errors.push(format!("{error:#}")),
            }
        }
        bail!("无法读取公共仓库版本信息：{}", errors.join("；"))
    }
    pub fn install(&mut self) -> Result<Status> {
        ensure!(!self.status()?.running, "请先停止服务再安装或更新。");
        progress(&self.root, "resolve", 2, "读取公共仓库版本")?;
        let (manifest, _, _) = self.fetch_manifest()?;
        if let Some(installed) = &self.state.installed {
            ensure!(installed.commit != manifest.commit, "当前源码版本已经安装，无需重复更新。");
            verify_files(&self.root.join("releases").join(&installed.release_id), &self.state.files)?;
        }
        let id = format!("source-{}", manifest.commit);
        let stage = self.root.join("staging").join(format!("{}-{}", id, SystemTime::now().duration_since(UNIX_EPOCH)?.as_millis()));
        fs::create_dir(&stage)?;
        progress(&self.root, "download", 5, "下载独立源码，不修改现有开发仓库")?;
        let source = self.download_source(&manifest.commit, &stage)?;
        let release = stage.join("release");
        fs::create_dir(&release)?;
        source_install::build_source(&source, &release, &self.root.join("cache"), &manifest.version, &manifest.commit,
            !matches!(self.state.config.source_mode, SourceMode::Official), &self.root.join("logs/source-build.log"))?;
        let executable = release.join(runtime_relative());
        fs::create_dir_all(executable.parent().context("Missing runtime parent")?)?;
        fs::write(&executable, include_bytes!(concat!(env!("OUT_DIR"), "/yingce-runtime.bin")))?;
        #[cfg(unix)] {
            use std::os::unix::fs::PermissionsExt;
            fs::set_permissions(&executable, fs::Permissions::from_mode(0o755))?;
        }
        progress(&self.root, "inventory", 84, "核对构建文件")?;
        let files = hash_inventory(&release)?;
        let target = self.root.join("releases").join(&id);
        if target.exists() { fs::rename(&target, stage.join("previous-failed-build")).context("无法保留前次失败构建，请确认相关进程已经退出")?; }
        fs::rename(&release, &target)?;
        let old_installed = self.state.installed.clone();
        let old_files = self.state.files.clone();
        let data_relative = format!("data-versions/{}-{}", id, SystemTime::now().duration_since(UNIX_EPOCH)?.as_millis());
        let new_data = self.root.join(&data_relative);
        progress(&self.root, "backup", 87, "复制一致数据快照，保留旧版本和原始数据")?;
        let mut data_lock = None;
        if let Some(old) = &old_installed {
            let old_data = self.installed_data(old)?;
            let lock = OpenOptions::new().create(true).truncate(false).read(true).write(true).open(old_data.join(".yingce-runtime.lock"))?;
            lock.try_lock_exclusive().context("旧版本仍在使用数据，不能并发更新")?;
            copy_snapshot(&old_data, &new_data)?;
            data_lock = Some(lock);
        } else { fs::create_dir(&new_data)?; }
        // Migrations run against the copy only. Original data and the persisted version pointer remain untouched until readiness.
        self.pending_restore = Some((old_installed.clone(), old_files.clone()));
        self.state.installed = Some(InstalledRelease { version: manifest.version.clone(), commit: manifest.commit.clone(),
            sequence: manifest.sequence, release_id: id, data_schema: manifest.migration.data_schema, data_dir: Some(data_relative) });
        self.state.files = files;
        let open_browser = self.state.config.open_browser;
        self.state.config.open_browser = false;
        progress(&self.root, "verify", 93, "启动真实后端并验证数据库和版本")?;
        let attempt = self.start_candidate();
        self.state.config.open_browser = open_browser;
        if let Err(error) = attempt {
            if let Err(stop_error) = self.stop() { bail!("候选服务启动失败且未能安全停止：{error:#}；{stop_error:#}"); }
            self.state.installed = old_installed;
            self.state.files = old_files;
            progress(&self.root, "failed", 93, "启动失败，原版本与原数据保持不变")?;
            return Err(error.context("启动验证失败，原版本与原数据未切换"));
        }
        if let Err(error) = self.save() {
            self.stop()?;
            self.state.installed = old_installed;
            self.state.files = old_files;
            return Err(error.context("无法提交版本指针，原版本数据保留"));
        }
        self.pending_restore = None;
        drop(data_lock);
        let source_target = self.root.join("sources").join(&manifest.commit);
        if !source_target.exists() { let _ = fs::rename(&source, &source_target); }
        progress(&self.root, "ready", 100, "源码安装完成，影策已经就绪")?;
        self.note("Source build installed; readiness verified; prior version and original data retained")?;
        if open_browser && self.open_browser().is_err() { self.note("Service ready; default browser failed, use Open button")?; }
        self.status()
    }
    fn download_source(&self, commit: &str, stage: &Path) -> Result<PathBuf> {
        ensure!(commit.len() == 40 && commit.bytes().all(|b| b.is_ascii_hexdigit()), "无效源码提交号");
        let official = format!("{REPOSITORY_URL}/archive/{commit}.zip");
        let archive = stage.join("source.zip");
        let sources = public_sources(&self.state.config, &official)?;
        let cached = self.root.join("cache").join(format!("source-{commit}.zip"));
        let record = cached.with_extension("json");
        let valid_cache = (|| -> Result<bool> {
            let metadata: serde_json::Value = serde_json::from_slice(&read_bounded(File::open(&record)?, 4096)?)?;
            if metadata["commit"].as_str() != Some(commit) || !sources.iter().any(|s| Some(s.as_str()) == metadata["source"].as_str()) { return Ok(false); }
            Ok(metadata["sha256"].as_str() == Some(file_sha256(&cached)?.as_str()) && zip::ZipArchive::new(File::open(&cached)?).is_ok())
        })().unwrap_or(false);
        let mut downloaded = valid_cache;
        if valid_cache { fs::copy(&cached, &archive)?; }
        let mut errors = Vec::new();
        for url in sources {
            if downloaded { break; }
            let origin = url.to_string();
            let result = (|| -> Result<()> {
                let response = self.client.get(url).timeout(Duration::from_secs(600)).send()?.error_for_status()?;
                ensure!(response.content_length().unwrap_or(0) <= MAX_ARCHIVE, "源码包过大");
                let mut file = OpenOptions::new().create_new(true).write(true).open(&archive)?;
                let mut response = response.take(MAX_ARCHIVE + 1);
                let mut count = 0u64;
                let mut buffer = [0u8; 65536];
                let mut last = Instant::now();
                loop {
                    let n = response.read(&mut buffer)?;
                    if n == 0 { break; }
                    count += n as u64;
                    ensure!(count <= MAX_ARCHIVE, "源码包下载超过上限");
                    file.write_all(&buffer[..n])?;
                    if last.elapsed() >= Duration::from_secs(1) {
                        progress(&self.root, "download", 5, &format!("正在下载独立源码，已接收 {} MiB", count / 1024 / 1024))?;
                        last = Instant::now();
                    }
                }
                ensure!(count > 0, "源码下载为空");
                file.sync_all()?;
                drop(file);
                zip::ZipArchive::new(File::open(&archive)?).context("源码下载被截断或不是完整 ZIP")?;
                fs::copy(&archive, &cached)?;
                fs::write(&record, serde_json::to_vec(&serde_json::json!({"commit":commit,"source":origin,"sha256":file_sha256(&cached)?}))?)?;
                Ok(())
            })();
            match result { Ok(()) => { downloaded = true; break; }, Err(error) => errors.push(format!("{error:#}")) }
            if archive.exists() { fs::remove_file(&archive)?; }
        }
        ensure!(downloaded, "源码下载失败，请检查网络或切换镜像：{}", errors.join("；"));
        let unpacked = stage.join("checkout");
        fs::create_dir(&unpacked)?;
        extract_zip(&archive, &unpacked)?;
        let entries = fs::read_dir(&unpacked)?.collect::<std::io::Result<Vec<_>>>()?;
        ensure!(entries.len() == 1 && entries[0].file_type()?.is_dir(), "公共源码包目录结构不符合预期");
        ensure!(entries[0].file_name() == std::ffi::OsStr::new(&format!("open-ai-canvas-{commit}")), "源码归档根目录与固定提交不匹配");
        let source = entries[0].path();
        ensure!(source.join("backend/go.mod").is_file() && source.join("web/bun.lock").is_file(), "源码缺少后端或锁文件");
        Ok(source)
    }
    fn installed_data(&self, installed: &InstalledRelease) -> Result<PathBuf> {
        match &installed.data_dir {
            Some(relative) => {
                let path = safe_relative(relative)?;
                ensure!(relative.starts_with("data-versions/"), "Invalid managed data directory");
                Ok(self.root.join(path))
            },
            None => Ok(self.root.join("data")),
        }
    }
    pub fn start(&mut self) -> Result<Status> {
        ensure!(self.pending_restore.is_none(), "候选版本尚未完成恢复，请先停止服务。");
        progress(&self.root, "integrity", 0, "正在核对已安装的运行文件，请稍候")?;
        self.start_candidate()
    }
    fn start_candidate(&mut self) -> Result<Status> {
        if self.status()?.running {
            return self.status();
        }
        let installed = self
            .state
            .installed
            .as_ref()
            .context("Install a verified release first")?;
        let release = self.root.join("releases").join(&installed.release_id);
        let expected_commit = installed.commit.clone();
        let expected_version = installed.version.clone();
        let data_dir = self.installed_data(installed)?;
        verify_files(&release, &self.state.files)?;
        progress(&self.root, "launch", if self.pending_restore.is_some() { 94 } else { 0 }, "正在启动本地后端，等待数据库和服务就绪")?;
        let port = self.state.config.port;
        let reservation = TcpListener::bind(("127.0.0.1", port))
            .context("Configured browser port is in use; it will not be changed automatically")?;
        let executable = release.join(runtime_relative());
        let log_path = self.root.join("logs/runtime.log");
        rotate_log(&log_path)?;
        let log = OpenOptions::new()
            .create(true)
            .append(true)
            .open(log_path)?;
        let mut token_bytes = [0u8; 32];
        getrandom::fill(&mut token_bytes).map_err(|e| anyhow!("无法生成本地停机凭据：{e}"))?;
        let shutdown_token = hex::encode(token_bytes);
        let mut command = Command::new(&executable);
        command
            .current_dir(&release)
            .arg("--port")
            .arg(port.to_string())
            .arg("--data-dir")
            .arg(&data_dir)
            .env_clear()
            .env("CANVAS_BACKEND_DATA_DIR", &data_dir)
            .env("YINGCE_RUNTIME_SHUTDOWN_TOKEN", &shutdown_token)
            .env("TEMP", self.root.join("temp"))
            .env("TMP", self.root.join("temp"))
            .env("TMPDIR", self.root.join("temp"))
            .env("HOME", self.root.join("runtime-home"))
            .env("USERPROFILE", self.root.join("runtime-home"))
            .stdin(Stdio::null())
            .stdout(log.try_clone()?)
            .stderr(log);
        // Windows runtime dependencies may require SystemRoot; never inherit secrets or arbitrary PATH.
        #[cfg(windows)]
        if let Some(value) = std::env::var_os("SystemRoot") {
            command.env("SystemRoot", value);
        }
        #[cfg(windows)]
        {
            use std::os::windows::process::CommandExt;
            // CREATE_NO_WINDOW: the console-subsystem gateway must not open a terminal; its
            // hidden console is inherited by the backend and Agent children.
            command.creation_flags(0x08000000);
        }
        #[cfg(unix)]
        {
            use std::os::unix::process::CommandExt;
            command.process_group(0);
        }
        #[cfg(windows)]
        let job = Job::new()?;
        drop(reservation);
        let mut child = command
            .spawn()
            .context("Could not start packaged runtime")?;
        #[cfg(windows)]
        {
            if let Err(e) = job.assign(&child) {
                let _ = child.kill();
                let _ = child.wait();
                return Err(e);
            }
            self.job = Some(job);
        }
        self.child = Some(child);
        self.shutdown_token = Some(shutdown_token);
        let health_client = Client::builder()
            .no_proxy()
            .redirect(reqwest::redirect::Policy::none())
            .timeout(Duration::from_secs(2))
            .build()?;
        let deadline = Instant::now() + Duration::from_secs(45);
        loop {
            if !self.status()?.running {
                bail!("Runtime exited before readiness; inspect runtime logs");
            }
            if let Ok(response) = health_client
                .get(format!("http://127.0.0.1:{port}/api/health/ready"))
                .send()
            {
                if response.status().is_success() {
                    if let Ok(raw) = read_bounded(response, 64 * 1024) {
                        if readiness_matches(&raw, &expected_version, &expected_commit) {
                            break;
                        }
                    }
                }
            }
            if Instant::now() >= deadline {
                self.stop()?;
                bail!("Runtime readiness/version handshake timed out; owned runtime stopped. Package build commit/version must match public release metadata.");
            }
            thread::sleep(Duration::from_millis(250));
        }
        self.note("Owned runtime ready on configured loopback port")?;
        if self.pending_restore.is_none() { progress(&self.root, "ready", 100, "影策服务已就绪")?; }
        if self.state.config.open_browser && self.open_browser().is_err() {
            self.note("Runtime is ready but default browser could not be opened")?;
        }
        self.status()
    }
    pub fn stop(&mut self) -> Result<Status> {
        if let Some(mut child) = self.child.take() {
            let result = (|| -> Result<()> {
                if child.try_wait()?.is_none() {
                    let token = self.shutdown_token.as_ref().context("缺少本地停机凭据，请勿强行更新数据")?;
                    let client = Client::builder().no_proxy().timeout(Duration::from_secs(5)).build()?;
                    let response = client.post(format!("http://127.0.0.1:{}/_launcher/stop", self.state.config.port))
                        .header("X-Yingce-Runtime-Token", token).send().context("无法请求本地服务正常退出")?;
                    ensure!(response.status().as_u16() == 202, "本地服务拒绝停机请求");
                    let deadline = Instant::now() + Duration::from_secs(60);
                    while child.try_wait()?.is_none() && Instant::now() < deadline { thread::sleep(Duration::from_millis(100)); }
                    ensure!(child.try_wait()?.is_some(), "服务尚未退出，请等待活动任务结束，不能继续更新");
                    let status = child.wait()?;
                    ensure!(status.success(), "运行时异常退出，请查看运行日志；原数据仍保留");
                }
                #[cfg(windows)] { self.job = None; }
                Ok(())
            })();
            if let Err(error) = result {
                if child.try_wait()?.is_none() { self.child = Some(child); }
                else { self.restore_pending(); }
                return Err(error);
            }
        }
        self.shutdown_token = None;
        self.restore_pending();
        self.status()
    }
    fn restore_pending(&mut self) {
        if let Some((installed, files)) = self.pending_restore.take() {
            self.state.installed = installed;
            self.state.files = files;
        }
    }
    pub fn open_browser(&mut self) -> Result<()> {
        ensure!(self.pending_restore.is_none(), "候选版本尚未提交，请先停止以恢复原版本。");
        ensure!(self.status()?.running, "Managed runtime is not running");
        open::that(format!("http://127.0.0.1:{}", self.state.config.port))
            .context("Could not open default browser")?;
        Ok(())
    }
    pub fn logs(&self) -> Result<Vec<String>> {
        // Runtime output is deliberately not exposed without a redaction/preview flow.
        let path = self.root.join("logs/launcher.log");
        if !path.exists() {
            return Ok(vec![]);
        }
        let bytes = read_bounded(File::open(path)?, MAX_LOG)?;
        Ok(String::from_utf8_lossy(&bytes)
            .lines()
            .rev()
            .take(200)
            .map(str::to_owned)
            .collect::<Vec<_>>()
            .into_iter()
            .rev()
            .collect())
    }
    fn note(&self, message: &str) -> Result<()> {
        let path = self.root.join("logs/launcher.log");
        rotate_log(&path)?;
        let mut file = OpenOptions::new().create(true).append(true).open(path)?;
        writeln!(
            file,
            "{} {message}",
            SystemTime::now().duration_since(UNIX_EPOCH)?.as_secs()
        )?;
        Ok(())
    }
    fn save(&self) -> Result<()> {
        let bytes = serialize_state(&self.state)?;
        let dir = self.root.join("state");
        let tmp = dir.join("state.next");
        let mut file = OpenOptions::new()
            .create_new(true)
            .write(true)
            .open(&tmp)
            .context("Pending state transaction exists; manual recovery required")?;
        file.write_all(&bytes)?;
        file.sync_all()?;
        drop(file);
        atomic_replace(&tmp, &dir.join("state.json"))?;
        #[cfg(unix)]
        File::open(&dir)?.sync_all()?;
        Ok(())
    }
}
impl Drop for Manager {
    fn drop(&mut self) {
        #[cfg(unix)]
        if let Some(child) = self.child.as_mut() {
            // Launcher exit is an explicit forced cleanup boundary; avoid leaving orphaned writers.
            unsafe {
                libc::kill(-(child.id() as i32), libc::SIGKILL);
            }
            let _ = child.wait();
        }
        #[cfg(windows)]
        {
            self.job = None;
            if let Some(child) = self.child.as_mut() {
                let _ = child.wait();
            }
        }
    }
}
fn serialize_state(state: &State) -> Result<Vec<u8>> {
    let bytes = serde_json::to_vec_pretty(state)?;
    ensure!(
        bytes.len() as u64 <= MAX_STATE,
        "Installed inventory exceeds reloadable state limit"
    );
    Ok(bytes)
}
fn copy_verified_archive(
    reader: impl Read,
    output: &mut impl Write,
    artifact: &Artifact,
) -> Result<()> {
    let mut reader = reader.take(artifact.size + 1);
    let mut hasher = Sha256::new();
    let mut count = 0u64;
    let mut buffer = [0; 65536];
    loop {
        let n = reader.read(&mut buffer)?;
        if n == 0 {
            break;
        }
        count += n as u64;
        ensure!(
            count <= artifact.size,
            "Archive exceeds repository-reported size"
        );
        output.write_all(&buffer[..n])?;
        hasher.update(&buffer[..n]);
    }
    ensure!(count == artifact.size, "Archive truncated");
    ensure!(
        hex::encode(hasher.finalize()) == artifact.sha256.to_lowercase(),
        "Archive SHA-256 mismatch"
    );
    Ok(())
}
fn readiness_matches(raw: &[u8], version: &str, commit: &str) -> bool {
    let Ok(body) = serde_json::from_slice::<serde_json::Value>(raw) else {
        return false;
    };
    body["code"].as_i64() == Some(0)
        && body["data"]["ready"].as_bool() == Some(true)
        && body["data"]["status"].as_str() == Some("ok")
        && body["data"]["build"]["commit"].as_str() == Some(commit)
        && body["data"]["build"]["version"].as_str() == Some(version)
}
fn public_sources(config: &Config, target: &str) -> Result<Vec<Url>> {
    let direct = Url::parse(target)?;
    ensure!(
        direct.scheme() == "https"
            && ["api.github.com", "github.com"].contains(&direct.host_str().unwrap_or("")),
        "Unsupported public repository host"
    );
    let mirror = || -> Result<Url> {
        let prefix = source_url(
            config
                .mirror_url
                .as_deref()
                .unwrap_or("https://gh-proxy.com/"),
        )?;
        ensure!(
            ["ghproxy.net", "ghfast.top", "gh-proxy.com"]
                .contains(&prefix.host_str().unwrap_or(""))
                && prefix.path() == "/"
                && prefix.port().is_none(),
            "请选择已支持的 GitHub 公共镜像"
        );
        Url::parse(&format!("{}{target}", prefix.as_str())).map_err(Into::into)
    };
    match config.source_mode {
        SourceMode::Official => Ok(vec![direct]),
        SourceMode::Mirror => Ok(vec![mirror()?]),
        SourceMode::Auto => Ok(vec![direct, mirror()?]),
    }
}
fn manifest_from_public(release: &PublicRelease) -> Result<ReleaseManifest> {
    platform()?;
    Ok(ReleaseManifest { schema_version: 1, sequence: SystemTime::now().duration_since(UNIX_EPOCH)?.as_secs(),
        expires_at: SystemTime::now().duration_since(UNIX_EPOCH)?.as_secs() + 3600,
        version: release.version.clone(), commit: release.commit.clone(), channel: "stable".into(),
        min_launcher_version: env!("CARGO_PKG_VERSION").into(), release_id: format!("source-{}", release.commit),
        artifacts: vec![], migration: Migration { data_schema: 1, requires_migration: false }, release_notes: release.notes.clone() })
}
fn public_install_reason(_manifest: &ReleaseManifest, _installed: bool) -> Option<String> { None }
fn progress(root: &Path, stage: &str, percent: u8, message: &str) -> Result<()> {
    let bytes = serde_json::to_vec(&serde_json::json!({ "stage": stage, "percent": percent, "message": message }))?;
    fs::write(root.join("logs/source-build.progress.json"), bytes)?;
    Ok(())
}
fn copy_snapshot(source: &Path, destination: &Path) -> Result<()> {
    reject_link(source)?;
    ensure!(!destination.exists(), "快照目录已存在，拒绝覆盖");
    fs::create_dir(destination)?;
    for entry in fs::read_dir(source)? {
        let entry = entry?;
        let path = entry.path();
        reject_link(&path)?;
        if entry.file_name() == ".yingce-runtime.lock" { continue; }
        let target = destination.join(entry.file_name());
        if entry.file_type()?.is_dir() { copy_snapshot(&path, &target)?; }
        else {
            ensure!(entry.file_type()?.is_file(), "数据快照不支持特殊文件");
            fs::copy(&path, &target)?;
            OpenOptions::new().read(true).write(true).open(target)?.sync_all()?;
        }
    }
    Ok(())
}
fn file_sha256(path: &Path) -> Result<String> {
    let mut file = File::open(path)?;
    let mut hash = Sha256::new(); let mut bytes = [0u8; 65536];
    loop { let n = file.read(&mut bytes)?; if n == 0 { break; } hash.update(&bytes[..n]); }
    Ok(hex::encode(hash.finalize()))
}
fn hash_inventory(root: &Path) -> Result<BTreeMap<String, String>> {
    let mut names = BTreeMap::new(); inventory_names(root, root, &mut names)?;
    let mut files = BTreeMap::new();
    for name in names.keys() {
        let mut file = File::open(root.join(name))?;
        let mut hash = Sha256::new(); let mut bytes = [0u8; 65536];
        loop { let n = file.read(&mut bytes)?; if n == 0 { break; } hash.update(&bytes[..n]); }
        files.insert(name.clone(), hex::encode(hash.finalize()));
    }
    Ok(files)
}
fn source_url(value: &str) -> Result<Url> {
    let url = Url::parse(value)?;
    ensure!(
        url.scheme() == "https"
            && url.host_str().is_some()
            && url.username().is_empty()
            && url.password().is_none()
            && url.query().is_none()
            && url.fragment().is_none()
            && url.path().ends_with('/'),
        "Source must be a credential-free HTTPS base URL ending in /, without query or fragment"
    );
    Ok(url)
}
fn validate_config(config: &Config) -> Result<()> {
    ensure!(
        config.port >= 1024,
        "Choose an unprivileged fixed port between 1024 and 65535"
    );
    if let Some(url) = &config.official_url {
        ensure!(
            url.trim_end_matches('/') == REPOSITORY_URL,
            "当前版本固定使用影策上游公共仓库"
        );
    }
    if let Some(url) = &config.mirror_url {
        source_url(url)?;
    }
    public_sources(config, DEFAULT_API_ROOT)?;
    Ok(())
}
fn validate_id(value: &str) -> Result<()> {
    ensure!(
        !value.is_empty()
            && value.len() <= 100
            && value
                .bytes()
                .all(|c| c.is_ascii_alphanumeric() || c == b'-' || c == b'_' || c == b'.')
            && value != "."
            && value != "..",
        "Invalid release identifier"
    );
    Ok(())
}
fn platform() -> Result<(&'static str, &'static str)> {
    platform_for(std::env::consts::OS, std::env::consts::ARCH)
}
fn platform_for(os: &str, arch: &str) -> Result<(&'static str, &'static str)> {
    match (os, arch) {
        ("windows", "x86_64") => Ok(("windows", "x86_64")),
        ("linux", "x86_64") => Ok(("linux", "x86_64")),
        ("macos", "x86_64") => Ok(("macos", "x86_64")),
        ("macos", "aarch64") => Ok(("macos", "aarch64")),
        _ => bail!("Unsupported platform/architecture: {os}/{arch}"),
    }
}
fn validate_manifest(m: &ReleaseManifest, highest: u64) -> Result<()> {
    ensure!(
        m.schema_version == 1 && m.channel == "stable",
        "Unsupported manifest schema or channel"
    );
    ensure!(
        m.sequence > 0 && m.sequence >= highest,
        "Old release manifest replay rejected"
    );
    let now = SystemTime::now().duration_since(UNIX_EPOCH)?.as_secs();
    ensure!(
        now > 1_700_000_000 && m.expires_at > now,
        "Manifest expired or system clock invalid"
    );
    ensure!(
        version_tuple(&m.min_launcher_version)? <= version_tuple(env!("CARGO_PKG_VERSION"))?,
        "A newer launcher is required"
    );
    validate_id(&m.release_id)?;
    validate_id(&m.version)?;
    ensure!(
        m.commit.len() == 40 && m.commit.bytes().all(|c| c.is_ascii_hexdigit()),
        "Expected complete 40-character commit"
    );
    ensure!(m.migration.data_schema > 0, "Data schema must be explicit");
    ensure!(
        m.artifacts.len() <= 32 && !m.artifacts.is_empty(),
        "Invalid artifact count"
    );
    for a in &m.artifacts {
        safe_relative(&a.path)?;
        ensure!(
            !a.path.contains('%') && !a.path.contains('?') && !a.path.contains('#'),
            "Artifact path must be literal relative path"
        );
        ensure!(
            a.kind == "zip" && a.size > 0 && a.size <= MAX_ARCHIVE,
            "Unsupported artifact kind or size"
        );
        ensure!(
            a.sha256.len() == 64 && a.sha256.bytes().all(|c| c.is_ascii_hexdigit()),
            "Invalid artifact SHA-256"
        );
    }
    Ok(())
}
fn version_tuple(value: &str) -> Result<(u64, u64, u64)> {
    let parts = value.split('.').collect::<Vec<_>>();
    ensure!(
        parts.len() == 3,
        "Launcher version must be major.minor.patch"
    );
    Ok((parts[0].parse()?, parts[1].parse()?, parts[2].parse()?))
}
fn select_artifact(m: &ReleaseManifest) -> Result<&Artifact> {
    let (os, arch) = platform()?;
    let mut matches = m
        .artifacts
        .iter()
        .filter(|a| a.platform == os && a.arch == arch);
    let first = matches
        .next()
        .context("No runtime artifact for this platform/architecture")?;
    ensure!(matches.next().is_none(), "Ambiguous platform artifact");
    Ok(first)
}
fn safe_relative(value: &str) -> Result<PathBuf> {
    ensure!(
        !value.is_empty()
            && !value.contains('\\')
            && !value.contains(':')
            && !value
                .chars()
                .any(|c| c.is_control() || matches!(c, '<' | '>' | '\"' | '|' | '?' | '*')),
        "Unsafe archive path"
    );
    let path = Path::new(value);
    ensure!(
        path.components().all(|c| matches!(c, Component::Normal(_))),
        "Unsafe relative path"
    );
    ensure!(
        value.split('/').count() <= 64 && value.len() <= 1024,
        "Archive path depth or length exceeds limit"
    );
    for part in value.split('/') {
        ensure!(
            !part.is_empty()
                && part != "."
                && part != ".."
                && !part.ends_with('.')
                && !part.ends_with(' '),
            "Nonportable archive path"
        );
        let stem = part.split('.').next().unwrap_or("").to_ascii_uppercase();
        ensure!(
            !matches!(stem.as_str(), "CON" | "PRN" | "AUX" | "NUL")
                && !(stem.len() == 4
                    && (stem.starts_with("COM") || stem.starts_with("LPT"))
                    && stem.as_bytes()[3].is_ascii_digit()),
            "Reserved archive path"
        );
    }
    Ok(path.to_owned())
}
fn reject_link(path: &Path) -> Result<()> {
    let meta = fs::symlink_metadata(path)?;
    ensure!(
        !meta.file_type().is_symlink(),
        "Managed path must not be a symbolic link"
    );
    #[cfg(windows)]
    {
        use std::os::windows::fs::MetadataExt;
        ensure!(
            meta.file_attributes() & 0x400 == 0,
            "Managed path must not be a reparse point"
        );
    }
    Ok(())
}
fn extract_zip(archive: &Path, destination: &Path) -> Result<BTreeMap<String, String>> {
    let mut zip = zip::ZipArchive::new(File::open(archive)?)?;
    ensure!(zip.len() <= MAX_FILES, "Archive has too many entries");
    let mut files = BTreeMap::new();
    let mut seen = std::collections::HashSet::new();
    let mut total = 0u64;
    for index in 0..zip.len() {
        let mut entry = zip.by_index(index)?;
        let name = entry.name().trim_end_matches('/').to_owned();
        let relative = safe_relative(&name)?;
        ensure!(
            seen.insert(name.to_ascii_lowercase()),
            "Duplicate/case-colliding archive entry"
        );
        let mode = entry.unix_mode().unwrap_or(0);
        let kind = mode & 0o170000;
        ensure!(
            kind == 0 || kind == 0o100000 || (kind == 0o040000 && entry.is_dir()),
            "Archive links or special files are forbidden"
        );
        let target = destination.join(relative);
        if entry.is_dir() {
            fs::create_dir_all(&target)?;
            continue;
        }
        total = total
            .checked_add(entry.size())
            .context("Expanded archive size overflow")?;
        ensure!(
            total <= MAX_EXPANDED && entry.size() <= MAX_ARCHIVE,
            "Expanded archive exceeds limit"
        );
        if let Some(parent) = target.parent() {
            fs::create_dir_all(parent)?;
        }
        let mut output = OpenOptions::new()
            .create_new(true)
            .write(true)
            .open(&target)?;
        let mut digest = Sha256::new();
        let mut count = 0u64;
        let expected = entry.size();
        let mut buffer = [0; 65536];
        loop {
            let n = entry.read(&mut buffer)?;
            if n == 0 {
                break;
            }
            count += n as u64;
            ensure!(
                count <= expected,
                "Archive entry expanded beyond declared size"
            );
            digest.update(&buffer[..n]);
            output.write_all(&buffer[..n])?;
        }
        ensure!(count == expected, "Truncated archive entry");
        output.sync_all()?;
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            fs::set_permissions(
                &target,
                fs::Permissions::from_mode(if mode & 0o111 != 0 { 0o755 } else { 0o644 }),
            )?;
        }
        files.insert(name, hex::encode(digest.finalize()));
    }
    Ok(files)
}
fn verify_files(root: &Path, files: &BTreeMap<String, String>) -> Result<()> {
    reject_link(root)?;
    ensure!(!files.is_empty(), "Installed file inventory missing");
    let mut discovered = BTreeMap::new();
    inventory_names(root, root, &mut discovered)?;
    ensure!(
        discovered.len() == files.len() && discovered.keys().eq(files.keys()),
        "Installed files added or removed; repair required"
    );
    let mut total = 0u64;
    // Every path is still link-checked and every file hashed on each start.
    // Shared parent directories need only one metadata query per verification pass.
    let mut checked = std::collections::HashSet::new();
    for (name, expected) in files {
        let relative = safe_relative(name)?;
        let mut path = root.to_owned();
        for component in relative.components() {
            path.push(component);
            if checked.insert(path.clone()) { reject_link(&path)?; }
        }
        let mut file = File::open(path)?;
        let size = file.metadata()?.len();
        total = total.checked_add(size).context("Installed size overflow")?;
        ensure!(
            size <= MAX_ARCHIVE && total <= MAX_EXPANDED,
            "Installed file exceeds size limit"
        );
        let mut digest = Sha256::new();
        let mut buffer = [0; 65536];
        loop {
            let n = file.read(&mut buffer)?;
            if n == 0 {
                break;
            }
            digest.update(&buffer[..n]);
        }
        ensure!(
            hex::encode(digest.finalize()) == *expected,
            "Installed file changed; repair required"
        );
    }
    Ok(())
}
fn inventory_names(root: &Path, dir: &Path, files: &mut BTreeMap<String, ()>) -> Result<()> {
    ensure!(
        dir.strip_prefix(root)?.components().count() <= 64,
        "Installed directory depth exceeds limit"
    );
    for entry in fs::read_dir(dir)? {
        let entry = entry?;
        let path = entry.path();
        reject_link(&path)?;
        if entry.file_type()?.is_dir() {
            inventory_names(root, &path, files)?;
        } else {
            ensure!(
                entry.file_type()?.is_file(),
                "Installed special file rejected"
            );
            ensure!(
                files.len() < MAX_FILES,
                "Installed file count exceeds limit"
            );
            files.insert(
                path.strip_prefix(root)?
                    .to_str()
                    .context("Non-UTF8 installed filename")?
                    .replace('\\', "/"),
                (),
            );
        }
    }
    Ok(())
}
fn runtime_relative() -> &'static str {
    if cfg!(windows) {
        "runtime/bin/yingce-runtime.exe"
    } else {
        "runtime/bin/yingce-runtime"
    }
}
fn read_bounded(mut reader: impl Read, limit: u64) -> Result<Vec<u8>> {
    let mut bytes = Vec::new();
    reader.by_ref().take(limit + 1).read_to_end(&mut bytes)?;
    ensure!(bytes.len() as u64 <= limit, "Input exceeds size limit");
    Ok(bytes)
}
fn rotate_log(path: &Path) -> Result<()> {
    if path.exists() {
        reject_link(path)?;
        if fs::metadata(path)?.len() >= MAX_LOG {
            let previous = path.with_extension("previous.log");
            if previous.exists() {
                fs::remove_file(&previous)?;
            }
            fs::rename(path, previous)?;
        }
    }
    Ok(())
}
#[cfg(not(windows))]
fn atomic_replace(from: &Path, to: &Path) -> Result<()> {
    fs::rename(from, to)?;
    Ok(())
}
#[cfg(windows)]
fn atomic_replace(from: &Path, to: &Path) -> Result<()> {
    use std::os::windows::ffi::OsStrExt;
    use windows_sys::Win32::Storage::FileSystem::{
        MoveFileExW, MOVEFILE_REPLACE_EXISTING, MOVEFILE_WRITE_THROUGH,
    };
    let from: Vec<u16> = from.as_os_str().encode_wide().chain(Some(0)).collect();
    let to: Vec<u16> = to.as_os_str().encode_wide().chain(Some(0)).collect();
    ensure!(
        unsafe {
            MoveFileExW(
                from.as_ptr(),
                to.as_ptr(),
                MOVEFILE_REPLACE_EXISTING | MOVEFILE_WRITE_THROUGH,
            )
        } != 0,
        "Atomic state replacement failed: {}",
        std::io::Error::last_os_error()
    );
    Ok(())
}
#[cfg(windows)]
struct Job(windows_sys::Win32::Foundation::HANDLE);
#[cfg(windows)]
unsafe impl Send for Job {}
#[cfg(windows)]
impl Job {
    fn new() -> Result<Self> {
        use windows_sys::Win32::System::JobObjects::*;
        let handle = unsafe { CreateJobObjectW(std::ptr::null(), std::ptr::null()) };
        ensure!(!handle.is_null(), "Could not create runtime job");
        let job = Self(handle);
        let mut info: JOBOBJECT_EXTENDED_LIMIT_INFORMATION = unsafe { std::mem::zeroed() };
        info.BasicLimitInformation.LimitFlags = JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE;
        ensure!(
            unsafe {
                SetInformationJobObject(
                    handle,
                    JobObjectExtendedLimitInformation,
                    &info as *const _ as *const _,
                    std::mem::size_of_val(&info) as u32,
                )
            } != 0,
            "Could not configure runtime job"
        );
        Ok(job)
    }
    fn assign(&self, child: &Child) -> Result<()> {
        use std::os::windows::io::AsRawHandle;
        ensure!(
            unsafe {
                windows_sys::Win32::System::JobObjects::AssignProcessToJobObject(
                    self.0,
                    child.as_raw_handle() as _,
                )
            } != 0,
            "Could not assign owned runtime to job"
        );
        Ok(())
    }
    fn terminate(&self) -> Result<()> {
        ensure!(
            unsafe { windows_sys::Win32::System::JobObjects::TerminateJobObject(self.0, 1) } != 0,
            "Could not terminate owned runtime job"
        );
        Ok(())
    }
}
#[cfg(windows)]
impl Drop for Job {
    fn drop(&mut self) {
        unsafe {
            windows_sys::Win32::Foundation::CloseHandle(self.0);
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn public_source_modes_are_strict_without_keys() {
        let mut config = Config::default();
        assert!(matches!(config.source_mode, SourceMode::Mirror), "default source is the mirror");
        config.source_mode = SourceMode::Official;
        let direct = public_sources(&config, DEFAULT_API_ROOT).unwrap();
        assert_eq!(direct.len(), 1);
        assert_eq!(direct[0].as_str(), DEFAULT_API_ROOT);
        config.source_mode = SourceMode::Mirror;
        let mirrors = public_sources(&config, DEFAULT_API_ROOT).unwrap();
        assert_eq!(mirrors.len(), 1);
        assert_eq!(mirrors[0].host_str(), Some("gh-proxy.com"));
        config.source_mode = SourceMode::Auto;
        assert_eq!(public_sources(&config, DEFAULT_API_ROOT).unwrap().len(), 2);
        config.mirror_url = Some("https://unapproved.example/".into());
        assert!(public_sources(&config, DEFAULT_API_ROOT).is_err());
    }
    #[test]
    fn repository_source_can_build_without_desktop_runtime_assets() {
        let release = PublicRelease {
            version: "main-aaaaaaa".into(),
            commit: "a".repeat(40),
            notes: "测试数据".into(),
        };
        let manifest = manifest_from_public(&release).unwrap();
        assert_eq!(manifest.version, "main-aaaaaaa");
        assert!(public_install_reason(&manifest, false).is_none());
    }
    #[test]
    #[ignore = "explicit public network probe, never runs in ordinary CI"]
    fn public_repository_live_probe() {
        let root = std::env::temp_dir().join(format!(
            "yingce-public-probe-{}-{}",
            std::process::id(),
            SystemTime::now()
                .duration_since(UNIX_EPOCH)
                .unwrap()
                .as_nanos()
        ));
        let mut manager = Manager::new(root.clone()).unwrap();
        if std::env::var_os("YINGCE_LAUNCHER_PROBE_MIRROR").is_some() {
            let mut config = manager.state.config.clone(); config.source_mode = SourceMode::Mirror; manager.configure(config).unwrap();
        }
        let result = manager.check_updates();
        drop(manager);
        let _ = fs::remove_dir_all(root);
        let info = result.unwrap();
        println!(
            "repository={} version={} canInstall={} reason={}",
            REPOSITORY_URL,
            info.manifest.version,
            info.can_install,
            info.reason.unwrap_or_default()
        );
    }
    #[test]
    #[ignore = "downloads and builds actual upstream, then starts real backend against isolated retained data"]
    fn source_install_live_smoke() {
        let root = PathBuf::from(std::env::var_os("YINGCE_LAUNCHER_SMOKE_ROOT").expect("explicit isolated smoke root required"));
        assert!(root.is_absolute());
        let mut manager = Manager::new(root.clone()).unwrap();
        let mut config = manager.state.config.clone();
        config.open_browser = false;
        if std::env::var_os("YINGCE_LAUNCHER_SMOKE_MIRROR").is_some() { config.source_mode = SourceMode::Auto; }
        manager.configure(config).unwrap();
        let result = if manager.state.installed.is_some() { manager.start() } else { manager.install() };
        if let Err(error) = result {
            for name in ["logs/source-build.log", "logs/runtime.log"] {
                if let Ok(text) = fs::read_to_string(root.join(name)) { eprintln!("{}\n{}", name, text.lines().rev().take(100).collect::<Vec<_>>().into_iter().rev().collect::<Vec<_>>().join("\n")); }
            }
            panic!("Live source install failed: {error:#}");
        }
        let status = manager.status().unwrap();
        assert!(status.running);
        println!("LIVE_READY version={} commit={} url={}", status.installed.as_ref().unwrap().version, status.installed.as_ref().unwrap().commit, status.url);
        let client = Client::builder().no_proxy().timeout(Duration::from_secs(10)).build().unwrap();
        let html = client.get(&status.url).send().unwrap().error_for_status().unwrap().text().unwrap();
        assert!(html.contains("<script") && html.contains("assets/"));
        manager.stop().unwrap();
        assert!(!manager.status().unwrap().running);
        println!("LIVE_STOPPED_CLEANLY");
        drop(manager);
        let mut reloaded = Manager::new(root).unwrap();
        reloaded.start().unwrap();
        reloaded.stop().unwrap();
        println!("LIVE_RESTARTED_WITH_PERSISTED_STATE");
    }
    #[test]
    fn snapshots_do_not_mutate_original_data_or_copy_live_lock() {
        let root = std::env::temp_dir().join(format!("yingce-snapshot-{}-{}", std::process::id(), SystemTime::now().duration_since(UNIX_EPOCH).unwrap().as_nanos()));
        let source = root.join("old"); let target = root.join("new");
        fs::create_dir_all(source.join("uploads")).unwrap();
        fs::write(source.join("uploads/asset"), b"original").unwrap();
        fs::write(source.join(".yingce-runtime.lock"), b"").unwrap();
        copy_snapshot(&source, &target).unwrap();
        assert!(!target.join(".yingce-runtime.lock").exists());
        fs::write(target.join("uploads/asset"), b"changed").unwrap();
        assert_eq!(fs::read(source.join("uploads/asset")).unwrap(), b"original");
        assert!(copy_snapshot(&source, &target).is_err());
        fs::remove_dir_all(root).unwrap();
    }
    #[test]
    fn stopping_uncommitted_candidate_restores_original_pointer() {
        let root = std::env::temp_dir().join(format!("yingce-pending-{}-{}", std::process::id(), SystemTime::now().duration_since(UNIX_EPOCH).unwrap().as_nanos()));
        let mut manager = Manager::new(root.clone()).unwrap();
        manager.pending_restore = Some((None, BTreeMap::new()));
        assert!(manager.start().is_err());
        assert!(manager.open_browser().is_err());
        manager.stop().unwrap();
        assert!(manager.pending_restore.is_none());
        assert!(manager.state.installed.is_none());
        drop(manager);
        fs::remove_dir_all(root).unwrap();
    }
    #[test]
    fn inventory_larger_than_manifest_limit_remains_reloadable() {
        let mut state = State::default();
        for i in 0..20_000 {
            state.files.insert(
                format!("runtime/agent/node_modules/package-{i}/index.js"),
                "a".repeat(64),
            );
        }
        let bytes = serialize_state(&state).unwrap();
        assert!(bytes.len() as u64 > MAX_MANIFEST);
        let loaded = read_bounded(std::io::Cursor::new(bytes), MAX_STATE).unwrap();
        let restored: State = serde_json::from_slice(&loaded).unwrap();
        assert_eq!(restored.files.len(), 20_000);
    }
    #[test]
    fn archive_copy_keeps_expected_identity_across_sources() {
        let bytes = b"same expected bytes from any approved source";
        let artifact = Artifact {
            platform: "windows".into(),
            arch: "x86_64".into(),
            path: "runtime.zip".into(),
            size: bytes.len() as u64,
            sha256: hex::encode(Sha256::digest(bytes)),
            kind: "zip".into(),
        };
        let mut output = Vec::new();
        assert!(copy_verified_archive(
            std::io::Cursor::new(b"wrong source"),
            &mut output,
            &artifact
        )
        .is_err());
        output.clear();
        copy_verified_archive(std::io::Cursor::new(bytes), &mut output, &artifact).unwrap();
        assert_eq!(output, bytes);
        output.clear();
        let mut oversized = bytes.to_vec();
        oversized.push(0);
        assert!(
            copy_verified_archive(std::io::Cursor::new(oversized), &mut output, &artifact).is_err()
        );
    }
    #[test]
    fn traversal_and_portable_names_rejected() {
        for name in [
            "../escape",
            "/absolute",
            "C:/x",
            "x/../y",
            "a\\b",
            "a//b",
            "./x",
            "NUL.txt",
            "a.",
            "COM1",
        ] {
            assert!(safe_relative(name).is_err(), "{name}");
        }
        assert!(safe_relative("runtime/bin/yingce-runtime").is_ok());
    }
    #[test]
    fn exact_platform_matrix() {
        assert!(platform_for("windows", "x86_64").is_ok());
        assert!(platform_for("macos", "aarch64").is_ok());
        assert!(platform_for("linux", "aarch64").is_err());
        assert!(platform_for("windows", "x86").is_err());
    }
    #[test]
    fn source_validation() {
        for value in [
            "http://example.org/",
            "https://user:secret@example.org/",
            "https://example.org/?key=x",
            "https://example.org/base",
        ] {
            assert!(source_url(value).is_err());
        }
        assert!(source_url("https://example.org/releases/").is_ok());
        assert!(validate_config(&Config {
            port: 0,
            ..Config::default()
        })
        .is_err());
    }
    #[test]
    fn size_bounds() {
        assert!(read_bounded(&b"12345"[..], 4).is_err());
    }
    #[test]
    fn readiness_requires_real_json_and_exact_build_identity() {
        let raw = br#"{"code":0,"data":{"ready":true,"status":"ok","build":{"version":"1.0.0","commit":"abc"}}}"#;
        assert!(readiness_matches(raw, "1.0.0", "abc"));
        assert!(!readiness_matches(raw, "1.0.1", "abc"));
        assert!(!readiness_matches(raw, "1.0.0", "other"));
        assert!(!readiness_matches(
            b"<html>SPA fallback</html>",
            "1.0.0",
            "abc"
        ));
        assert!(!readiness_matches(
            br#"{"code":0,"data":{"ready":false}}"#,
            "1.0.0",
            "abc"
        ));
    }
    #[test]
    fn invalid_manifest_and_replay_rejected() {
        let mut manifest = ReleaseManifest {
            schema_version: 1,
            sequence: 2,
            expires_at: SystemTime::now()
                .duration_since(UNIX_EPOCH)
                .unwrap()
                .as_secs()
                + 3600,
            version: "1.0.0".into(),
            commit: "a".repeat(40),
            channel: "stable".into(),
            min_launcher_version: "0.1.0".into(),
            release_id: "release-2".into(),
            artifacts: vec![Artifact {
                platform: "windows".into(),
                arch: "x86_64".into(),
                path: "packages/runtime.zip".into(),
                size: 1,
                sha256: "a".repeat(64),
                kind: "zip".into(),
            }],
            migration: Migration {
                data_schema: 1,
                requires_migration: false,
            },
            release_notes: String::new(),
        };
        assert!(validate_manifest(&manifest, 2).is_ok());
        assert!(validate_manifest(&manifest, 3).is_err());
        manifest.expires_at = 0;
        assert!(validate_manifest(&manifest, 0).is_err());
        manifest.schema_version = 99;
        assert!(validate_manifest(&manifest, 0).is_err());
    }
    #[test]
    fn version_validation() {
        assert!(version_tuple("1.2").is_err());
        assert!(version_tuple("1.2.3-dev").is_err());
        assert!(version_tuple("1.2.3").unwrap() > version_tuple("0.9.9").unwrap());
    }
}
