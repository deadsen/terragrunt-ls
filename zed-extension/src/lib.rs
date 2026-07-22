use zed::settings::CommandSettings;
use zed_extension_api as zed;

fn resolve_command(
    configured: Option<CommandSettings>,
    path_binary: Option<String>,
) -> zed::Result<zed::Command> {
    let configured = configured.unwrap_or(CommandSettings {
        path: None,
        arguments: None,
        env: None,
    });
    let command = configured.path.or(path_binary).ok_or_else(|| {
        "The LSP for Terragrunt 'terragrunt-ls' is not installed or configured".to_string()
    })?;

    Ok(zed::Command {
        command,
        args: configured.arguments.unwrap_or_default(),
        env: configured.env.unwrap_or_default().into_iter().collect(),
    })
}

struct TerragruntLsExtension;

impl zed::Extension for TerragruntLsExtension {
    fn new() -> Self {
        Self
    }
    fn language_server_command(
        &mut self,
        language_server_id: &zed::LanguageServerId,
        worktree: &zed::Worktree,
    ) -> zed::Result<zed::Command> {
        let settings =
            zed::settings::LspSettings::for_worktree(language_server_id.as_ref(), worktree)?;

        resolve_command(settings.binary, worktree.which("terragrunt-ls"))
    }
}

zed::register_extension!(TerragruntLsExtension);

#[cfg(test)]
mod tests {
    use super::*;
    use std::collections::HashMap;

    #[test]
    fn configured_binary_wins() {
        let configured = CommandSettings {
            path: Some("/tmp/local/terragrunt-ls".into()),
            arguments: Some(vec!["--trace".into()]),
            env: Some(HashMap::from([("TG_LOG".into(), "debug".into())])),
        };

        let command =
            resolve_command(Some(configured), Some("/usr/bin/terragrunt-ls".into())).unwrap();

        assert_eq!(command.command, "/tmp/local/terragrunt-ls");
        assert_eq!(command.args, vec!["--trace"]);
        assert!(command.env.contains(&("TG_LOG".into(), "debug".into())));
    }

    #[test]
    fn path_binary_is_used_without_configuration() {
        let command = resolve_command(None, Some("/usr/bin/terragrunt-ls".into())).unwrap();

        assert_eq!(command.command, "/usr/bin/terragrunt-ls");
        assert!(command.args.is_empty());
        assert!(command.env.is_empty());
    }

    #[test]
    fn missing_binary_is_reported() {
        let error = resolve_command(None, None).unwrap_err();

        assert!(error.contains("terragrunt-ls"));
    }
}
