# Terragrunt Language Server

This is a language server for [Terragrunt](https://terragrunt.gruntwork.io/), with native integrations for Zed and Visual Studio Code.

It's a work in progress, and we're looking to start coordination with the Terragrunt community to make this the best possible language server for Terragrunt.

## Capabilities

The capabilities of this language server are documented in the [server capabilities documentation](./docs/server-capabilities.md).

Highlights include nested-local hover, definition navigation for locals/includes/dependencies/files, references and rename for Terragrunt symbols, range-aware completion, semantic diagnostics, formatting, and an explicit dependency-output code action.

The editor extensions activate by default only for `terragrunt.hcl`, `root.hcl`, `terragrunt.stack.hcl`, and `terragrunt.values.hcl`. Additional filenames use each editor's native language-association setting.

## Setup

For instructions on how to setup the Terragrunt Language Server in your editor, see the [setup documentation](./docs/setup.md).

Both integrations run a local build of `terragrunt-ls`; neither downloads a language-server binary at runtime.

## Contributions

Contributions are welcome, though the maintainers request your patience and understanding, as this is not a project we can dedicate a lot of time to.

If you would like to contribute, please read the [contributing documentation](./docs/contributing.md) file.

## Special Thanks

A special thanks also goes to [jowharshamshiri](https://github.com/jowharshamshiri) for getting the ball rolling on [tg-hcl-lsp](https://github.com/jowharshamshiri/tg-hcl-lsp) and [mightyguava](https://github.com/mightyguava) for [terragrunt-langserver](https://github.com/mightyguava/terragrunt-langserver), the first community supported Terragrunt Language Servers. Seeing the community commit to creating Language Servers on their own convinced the maintainers of Terragrunt to create and maintain this official version.

This one was created by learning from [tjdevries](https://github.com/tjdevries)'s [educational-lsp](https://github.com/tjdevries/educationalsp), and that educational example served as a great Golang-based starting point for this project.
