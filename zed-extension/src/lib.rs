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
    use tree_sitter::{Parser, Query, QueryCursor, StreamingIterator};

    const HIGHLIGHTS_QUERY: &str = include_str!("../languages/terragrunt/highlights.scm");
    const LANGUAGE_CONFIG: &str = include_str!("../languages/terragrunt/config.toml");

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
