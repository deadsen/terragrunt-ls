use sha2::{Digest, Sha256};
use std::{
    fs,
    io::{self, Cursor},
    path::Path,
};
use zed::settings::CommandSettings;
use zed_extension_api as zed;

const GITHUB_REPOSITORY: &str = "deadsen/terragrunt-ls";
const SERVER_NAME: &str = "terragrunt-ls";
const CHECKSUMS_ASSET: &str = "SHA256SUMS";
const INSTALL_DIRECTORY_PREFIX: &str = "terragrunt-ls-v";

#[derive(Debug, PartialEq)]
struct ReleasePlatform {
    asset_name: String,
    binary_name: &'static str,
}

fn resolve_command(
    configured: Option<CommandSettings>,
    path_binary: Option<String>,
    managed_binary: Option<String>,
) -> zed::Result<zed::Command> {
    let configured = configured.unwrap_or(CommandSettings {
        path: None,
        arguments: None,
        env: None,
    });
    let command = configured
        .path
        .or(path_binary)
        .or(managed_binary)
        .ok_or_else(|| {
            "The LSP for Terragrunt 'terragrunt-ls' is not installed or configured".to_string()
        })?;

    Ok(zed::Command {
        command,
        args: configured.arguments.unwrap_or_default(),
        env: configured.env.unwrap_or_default().into_iter().collect(),
    })
}

fn release_platform(os: zed::Os, architecture: zed::Architecture) -> zed::Result<ReleasePlatform> {
    let os_name = match os {
        zed::Os::Mac => "darwin",
        zed::Os::Linux => "linux",
        zed::Os::Windows => "windows",
    };
    let architecture_name = match architecture {
        zed::Architecture::Aarch64 => "arm64",
        zed::Architecture::X8664 => "amd64",
        zed::Architecture::X86 => {
            return Err("terragrunt-ls does not publish 32-bit release binaries".to_string())
        }
    };
    let binary_name = if os == zed::Os::Windows {
        "terragrunt-ls.exe"
    } else {
        SERVER_NAME
    };

    Ok(ReleasePlatform {
        asset_name: format!("{SERVER_NAME}_{os_name}_{architecture_name}.zip"),
        binary_name,
    })
}

fn checksum_for_asset(manifest: &str, asset_name: &str) -> zed::Result<String> {
    manifest
        .lines()
        .find_map(|line| {
            let mut fields = line.split_whitespace();
            let checksum = fields.next()?;
            let filename = fields.next()?.trim_start_matches('*');
            (filename == asset_name).then(|| checksum.to_string())
        })
        .ok_or_else(|| format!("{CHECKSUMS_ASSET} does not contain a checksum for {asset_name}"))
}

fn verify_checksum(bytes: &[u8], expected: &str) -> zed::Result<()> {
    let actual = format!("{:x}", Sha256::digest(bytes));
    if actual.eq_ignore_ascii_case(expected) {
        Ok(())
    } else {
        Err(format!(
            "checksum verification failed: expected {expected}, downloaded {actual}"
        ))
    }
}

fn fetch_bytes(url: &str) -> zed::Result<Vec<u8>> {
    zed::http_client::HttpRequest::builder()
        .method(zed::http_client::HttpMethod::Get)
        .url(url)
        .redirect_policy(zed::http_client::RedirectPolicy::FollowAll)
        .build()?
        .fetch()
        .map(|response| response.body)
}

fn extract_binary(archive_bytes: Vec<u8>, binary_name: &str, destination: &str) -> zed::Result<()> {
    let mut archive = zip::ZipArchive::new(Cursor::new(archive_bytes))
        .map_err(|error| format!("failed to read release archive: {error}"))?;
    let mut archived_binary = archive
        .by_name(binary_name)
        .map_err(|error| format!("release archive does not contain {binary_name}: {error}"))?;
    let destination_path = Path::new(destination);
    let parent = destination_path
        .parent()
        .ok_or_else(|| format!("invalid installation path: {destination}"))?;
    fs::create_dir_all(parent)
        .map_err(|error| format!("failed to create {}: {error}", parent.display()))?;

    let temporary_path = parent.join(format!(".{binary_name}.download"));
    let mut temporary_file = fs::File::create(&temporary_path).map_err(|error| {
        format!(
            "failed to create temporary binary {}: {error}",
            temporary_path.display()
        )
    })?;
    if let Err(error) = io::copy(&mut archived_binary, &mut temporary_file) {
        drop(temporary_file);
        let _ = fs::remove_file(&temporary_path);
        return Err(format!(
            "failed to extract {binary_name} to {}: {error}",
            temporary_path.display()
        ));
    }
    drop(temporary_file);
    if let Err(error) = fs::rename(&temporary_path, destination_path) {
        let _ = fs::remove_file(&temporary_path);
        return Err(format!(
            "failed to install {binary_name} at {}: {error}",
            destination_path.display()
        ));
    }
    Ok(())
}

fn remove_old_installations(current_directory: &str) {
    let Ok(entries) = fs::read_dir(".") else {
        return;
    };

    for entry in entries.flatten() {
        let path = entry.path();
        let Some(name) = path.file_name().and_then(|name| name.to_str()) else {
            continue;
        };
        if name != current_directory && name.starts_with(INSTALL_DIRECTORY_PREFIX) && path.is_dir()
        {
            let _ = fs::remove_dir_all(path);
        }
    }
}

fn existing_binary(path: Option<&str>) -> Option<String> {
    path.filter(|path| Path::new(path).is_file())
        .map(str::to_string)
}

struct TerragruntLsExtension {
    cached_binary_path: Option<String>,
}

impl zed::Extension for TerragruntLsExtension {
    fn new() -> Self {
        Self {
            cached_binary_path: None,
        }
    }

    fn language_server_command(
        &mut self,
        language_server_id: &zed::LanguageServerId,
        worktree: &zed::Worktree,
    ) -> zed::Result<zed::Command> {
        let settings =
            zed::settings::LspSettings::for_worktree(language_server_id.as_ref(), worktree)?;

        let configured_path = settings
            .binary
            .as_ref()
            .and_then(|binary| binary.path.clone());
        let path_binary = configured_path
            .is_none()
            .then(|| worktree.which(SERVER_NAME))
            .flatten();

        if configured_path.is_some() || path_binary.is_some() {
            return resolve_command(settings.binary, path_binary, None);
        }

        let managed_binary = self.managed_binary_path(language_server_id)?;
        resolve_command(settings.binary, None, Some(managed_binary))
    }
}

impl TerragruntLsExtension {
    fn managed_binary_path(
        &mut self,
        language_server_id: &zed::LanguageServerId,
    ) -> zed::Result<String> {
        if let Some(path) = existing_binary(self.cached_binary_path.as_deref()) {
            return Ok(path);
        }

        zed::set_language_server_installation_status(
            language_server_id,
            &zed::LanguageServerInstallationStatus::CheckingForUpdate,
        );

        let result = self.install_latest_release(language_server_id);
        match &result {
            Ok(_) => zed::set_language_server_installation_status(
                language_server_id,
                &zed::LanguageServerInstallationStatus::None,
            ),
            Err(error) => zed::set_language_server_installation_status(
                language_server_id,
                &zed::LanguageServerInstallationStatus::Failed(error.clone()),
            ),
        }
        result
    }

    fn install_latest_release(
        &mut self,
        language_server_id: &zed::LanguageServerId,
    ) -> zed::Result<String> {
        let release = zed::latest_github_release(
            GITHUB_REPOSITORY,
            zed::GithubReleaseOptions {
                require_assets: true,
                pre_release: false,
            },
        )?;
        let (os, architecture) = zed::current_platform();
        let platform = release_platform(os, architecture)?;
        let archive_asset = release
            .assets
            .iter()
            .find(|asset| asset.name == platform.asset_name)
            .ok_or_else(|| {
                format!(
                    "GitHub release {} does not contain {}",
                    release.version, platform.asset_name
                )
            })?;
        let checksums_asset = release
            .assets
            .iter()
            .find(|asset| asset.name == CHECKSUMS_ASSET)
            .ok_or_else(|| {
                format!(
                    "GitHub release {} does not contain {CHECKSUMS_ASSET}",
                    release.version
                )
            })?;

        let version = release
            .version
            .chars()
            .map(|character| {
                if character.is_ascii_alphanumeric() || matches!(character, '.' | '-' | '_') {
                    character
                } else {
                    '_'
                }
            })
            .collect::<String>();
        let install_directory = format!("{SERVER_NAME}-{version}");
        let binary_path = format!("{install_directory}/{}", platform.binary_name);

        if Path::new(&binary_path).is_file() {
            self.cached_binary_path = Some(binary_path.clone());
            return Ok(binary_path);
        }

        zed::set_language_server_installation_status(
            language_server_id,
            &zed::LanguageServerInstallationStatus::Downloading,
        );

        let checksum_manifest = String::from_utf8(fetch_bytes(&checksums_asset.download_url)?)
            .map_err(|error| format!("{CHECKSUMS_ASSET} is not valid UTF-8: {error}"))?;
        let expected_checksum = checksum_for_asset(&checksum_manifest, &platform.asset_name)?;
        let archive_bytes = fetch_bytes(&archive_asset.download_url)?;
        verify_checksum(&archive_bytes, &expected_checksum)?;
        extract_binary(archive_bytes, platform.binary_name, &binary_path)?;
        if os != zed::Os::Windows {
            if let Err(error) = zed::make_file_executable(&binary_path) {
                let _ = fs::remove_file(&binary_path);
                return Err(error);
            }
        }

        remove_old_installations(&install_directory);
        self.cached_binary_path = Some(binary_path.clone());
        Ok(binary_path)
    }
}

zed::register_extension!(TerragruntLsExtension);

#[cfg(test)]
mod tests {
    use super::*;
    use std::collections::HashMap;
    use std::io::Write;
    use std::time::{SystemTime, UNIX_EPOCH};
    use tree_sitter::{Parser, Query, QueryCursor, StreamingIterator};

    const HIGHLIGHTS_QUERY: &str = include_str!("../languages/terragrunt/highlights.scm");
    const LANGUAGE_CONFIG: &str = include_str!("../languages/terragrunt/config.toml");
    const MANAGED_BINARY_PATH: &str = "managed/terragrunt-ls";

    #[test]
    fn configured_binary_wins() {
        let configured = CommandSettings {
            path: Some("/tmp/local/terragrunt-ls".into()),
            arguments: Some(vec!["--trace".into()]),
            env: Some(HashMap::from([("TG_LOG".into(), "debug".into())])),
        };

        let command = resolve_command(
            Some(configured),
            Some("/usr/bin/terragrunt-ls".into()),
            Some(MANAGED_BINARY_PATH.into()),
        )
        .unwrap();

        assert_eq!(command.command, "/tmp/local/terragrunt-ls");
        assert_eq!(command.args, vec!["--trace"]);
        assert!(command.env.contains(&("TG_LOG".into(), "debug".into())));
    }

    #[test]
    fn path_binary_wins_over_managed_binary() {
        let command = resolve_command(
            None,
            Some("/usr/bin/terragrunt-ls".into()),
            Some(MANAGED_BINARY_PATH.into()),
        )
        .unwrap();

        assert_eq!(command.command, "/usr/bin/terragrunt-ls");
        assert!(command.args.is_empty());
        assert!(command.env.is_empty());
    }

    #[test]
    fn managed_binary_is_used_as_the_last_fallback() {
        let command = resolve_command(None, None, Some(MANAGED_BINARY_PATH.into())).unwrap();

        assert_eq!(command.command, MANAGED_BINARY_PATH);
    }

    #[test]
    fn managed_binary_preserves_configured_arguments_and_environment() {
        let configured = CommandSettings {
            path: None,
            arguments: Some(vec!["--trace".into()]),
            env: Some(HashMap::from([("TG_LOG".into(), "debug".into())])),
        };

        let command =
            resolve_command(Some(configured), None, Some(MANAGED_BINARY_PATH.into())).unwrap();

        assert_eq!(command.args, vec!["--trace"]);
        assert!(command.env.contains(&("TG_LOG".into(), "debug".into())));
    }

    #[test]
    fn release_asset_names_match_supported_platforms() {
        let cases = [
            (
                zed::Os::Mac,
                zed::Architecture::Aarch64,
                "terragrunt-ls_darwin_arm64.zip",
                "terragrunt-ls",
            ),
            (
                zed::Os::Mac,
                zed::Architecture::X8664,
                "terragrunt-ls_darwin_amd64.zip",
                "terragrunt-ls",
            ),
            (
                zed::Os::Linux,
                zed::Architecture::Aarch64,
                "terragrunt-ls_linux_arm64.zip",
                "terragrunt-ls",
            ),
            (
                zed::Os::Linux,
                zed::Architecture::X8664,
                "terragrunt-ls_linux_amd64.zip",
                "terragrunt-ls",
            ),
            (
                zed::Os::Windows,
                zed::Architecture::Aarch64,
                "terragrunt-ls_windows_arm64.zip",
                "terragrunt-ls.exe",
            ),
            (
                zed::Os::Windows,
                zed::Architecture::X8664,
                "terragrunt-ls_windows_amd64.zip",
                "terragrunt-ls.exe",
            ),
        ];

        for (os, architecture, expected_asset, expected_binary) in cases {
            let platform = release_platform(os, architecture).unwrap();
            assert_eq!(platform.asset_name, expected_asset);
            assert_eq!(platform.binary_name, expected_binary);
        }
    }

    #[test]
    fn unsupported_32_bit_architecture_is_reported() {
        let error = release_platform(zed::Os::Linux, zed::Architecture::X86).unwrap_err();

        assert!(error.contains("32-bit"));
    }

    #[test]
    fn checksum_manifest_selects_the_requested_asset() {
        let manifest = "\
aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  other.zip
0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef  terragrunt-ls_darwin_arm64.zip
";

        assert_eq!(
            checksum_for_asset(manifest, "terragrunt-ls_darwin_arm64.zip").unwrap(),
            "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
        );
    }

    #[test]
    fn checksum_mismatch_is_rejected() {
        let error = verify_checksum(
            b"terragrunt-ls archive",
            "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
        )
        .unwrap_err();

        assert!(error.contains("checksum"));
    }

    #[test]
    fn matching_checksum_is_accepted() {
        verify_checksum(
            b"abc",
            "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad",
        )
        .unwrap();
    }

    #[test]
    fn only_the_expected_binary_is_extracted() {
        let mut archive = Cursor::new(Vec::new());
        {
            let mut writer = zip::ZipWriter::new(&mut archive);
            let options = zip::write::SimpleFileOptions::default()
                .compression_method(zip::CompressionMethod::Deflated);
            writer.start_file("terragrunt-ls", options).unwrap();
            writer.write_all(b"server binary").unwrap();
            writer.start_file("../unexpected", options).unwrap();
            writer.write_all(b"must not be extracted").unwrap();
            writer.finish().unwrap();
        }

        let unique = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap()
            .as_nanos();
        let directory = std::env::temp_dir().join(format!("terragrunt-ls-zed-test-{unique}"));
        let destination = directory.join("terragrunt-ls");

        extract_binary(
            archive.into_inner(),
            "terragrunt-ls",
            destination.to_str().unwrap(),
        )
        .unwrap();

        assert_eq!(fs::read(&destination).unwrap(), b"server binary");
        assert!(!directory.join("unexpected").exists());
        assert_eq!(
            existing_binary(destination.to_str()),
            Some(destination.to_string_lossy().into_owned())
        );
        fs::remove_dir_all(directory).unwrap();
    }

    #[test]
    fn traversal_levels_have_distinct_highlight_scopes() {
        let source = r#"
inputs = {
  cluster = dependency.ecs-task-execution-role.outputs.cluster_name
  tags    = local.account.tags
  env     = include.root.locals.environment
  deep    = dependency.one.two.three.four.five.six.seven.eight
}
"#;

        let captures = highlight_captures(source);

        for expected in [
            ("dependency", "type"),
            ("ecs-task-execution-role", "property"),
            ("outputs", "attribute"),
            ("cluster_name", "variable.special"),
            ("local", "type"),
            ("account", "property"),
            ("tags", "attribute"),
            ("include", "type"),
            ("root", "property"),
            ("locals", "attribute"),
            ("environment", "variable.special"),
            ("one", "property"),
            ("two", "attribute"),
            ("three", "variable.special"),
            ("four", "property"),
            ("five", "attribute"),
            ("six", "variable.special"),
            ("seven", "property"),
            ("eight", "attribute"),
        ] {
            assert!(
                captures
                    .iter()
                    .any(|capture| capture.0 == expected.0 && capture.1 == expected.1),
                "missing capture {expected:?}; got {captures:?}"
            );
        }

        assert!(
            captures.iter().all(|capture| capture.1 != "tag"),
            "traversal depth highlighting must not repurpose tag captures: {captures:?}"
        );
    }

    #[test]
    fn hyphenated_identifiers_are_single_editor_words() {
        assert!(
            LANGUAGE_CONFIG
                .lines()
                .any(|line| line.trim() == r#"word_characters = ["-"]"#),
            "Terragrunt language config must include '-' in word_characters"
        );
    }

    fn highlight_captures(source: &str) -> Vec<(String, String)> {
        let language = tree_sitter_hcl::LANGUAGE.into();
        let mut parser = Parser::new();
        parser
            .set_language(&language)
            .expect("HCL grammar should load");
        let tree = parser
            .parse(source, None)
            .expect("Terragrunt fixture should parse");
        assert!(!tree.root_node().has_error());

        let query = Query::new(&language, HIGHLIGHTS_QUERY)
            .expect("Terragrunt highlights query should compile");
        let capture_names = query.capture_names();
        let mut cursor = QueryCursor::new();
        let mut matches = cursor.matches(&query, tree.root_node(), source.as_bytes());
        let mut captures = Vec::new();

        while let Some(query_match) = matches.next() {
            captures.extend(query_match.captures.iter().map(|capture| {
                let range = capture.node.byte_range();
                (
                    source[range].to_string(),
                    capture_names[capture.index as usize].to_string(),
                )
            }));
        }

        captures
    }
}
