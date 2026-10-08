# Discordo &middot; [![discord](https://img.shields.io/discord/1297292231299956788?color=5865F2&logo=discord&logoColor=white)](https://discord.com/invite/VzF9UFn2aB) [![ci](https://github.com/ayn2op/discordo/actions/workflows/ci.yml/badge.svg)](https://github.com/ayn2op/discordo/actions/workflows/ci.yml) [![license](https://img.shields.io/github/license/ayn2op/discordo?logo=github)](https://github.com/ayn2op/discordo/blob/master/LICENSE)

Discordo is a lightweight, secure, and feature-rich Discord terminal client.

![Preview](.github/preview.png)
![Picker](.github/picker.png)

## Installation

### Prebuilt binaries

You can download and install a [prebuilt binary here](https://nightly.link/ayn2op/discordo/workflows/ci/main) for Windows, macOS, or Linux.

### Package managers

- Arch Linux: `yay -S discordo-git`
- Gentoo (available on the guru repos as a live ebuild): `emerge net-im/discordo`
- FreeBSD: `pkg install discordo` or via the ports system `make -C /usr/ports/net-im/discordo install clean`
- Nix: Add `pkgs.discordo` to `environment.systemPackages` or `home.packages`
- Android (Termux): `pkg install discordo`

- Windows (Scoop):

```sh
scoop bucket add vvxrtues https://github.com/vvirtues/bucket
scoop install discordo
```

### Building from source

```bash
git clone https://github.com/ayn2op/discordo
cd discordo
go build .
```

## Usage

### Password (UI, recommended)

1. Run the `discordo` executable with no arguments.

2. Open the "Password" tab, enter your email address or E.164-formatted phone number and password, then click the "Login" button.

### QR (UI)

1. Run the `discordo` executable with no arguments.

2. Click on the "Login with QR" button.

3. Follow the instructions in the QR Login screen.

### Token (UI)

1. Run the `discordo` executable with no arguments.

2. Enter your token and click on the "Login" button to save it.

### Token (environment variable)

Set the value of the `DISCORDO_TOKEN` environment variable to the authentication token to log in with.

```sh
DISCORDO_TOKEN="OTI2MDU5NTQxNDE2Nzc5ODA2.Yc2KKA.2iZ-5JxgxG-9Ub8GHzBSn-NJjNg" discordo
```

## Low-data fork

Image previews are off by default. To enable them, set `preview = true` in `[attachments]`.

Downloaded history and incoming gateway messages are saved in an account-specific bbolt database. This includes messages received in other channels, whether or not they produce a notification. Cached history is reused across channel switches and restarts; missing ranges and newer messages are fetched as needed. Edits received while connected update messages in the active memory cache. Received deletions also remove the saved messages.

Message databases are stored in:

- Linux/BSD: `$XDG_DATA_HOME/discordo/messages/<user-id>.db`, or `~/.local/share/discordo/messages/<user-id>.db`
- macOS: `~/Library/Application Support/discordo/messages/<user-id>.db`
- Windows: `%AppData%/discordo/messages/<user-id>.db`

Writes use synced transactions, including message rows and history coverage. On Unix, the database files have mode `0600` and their directory has mode `0700`. Storage errors appear in the message pane footer and the log.

Messages and notification-setting changes arrive through Discord's live gateway. Guild/channel notification settings configured on the phone or web apply here too. Desktop popups require terminal notification support. Phone-specific push delivery and idle timing are separate from desktop notifications.

Reaction updates are discarded before state handling; reactions are neither displayed nor stored. Discord may still send their gateway payloads.

## Configuration

The configuration file allows you to configure and customize the behavior, keybindings, and theme of the application.

- Unix: `$XDG_CONFIG_HOME/discordo/config.toml` or `$HOME/.config/discordo/config.toml`
- Darwin: `$HOME/Library/Application Support/discordo/config.toml`
- Windows: `%AppData%/discordo/config.toml`

Discordo uses the default configuration if a configuration file is not found in the aforementioned path; however, the default configuration file is not written to the path. [The configuration reference can be found here](./docs/config.md).

## License

Copyright (C) 2025-present ayn2op

This project is licensed under the GNU General Public License v3.0 (GPL-3.0).
See the [LICENSE](./LICENSE) file for the full license text.

# Disclaimer

> [!IMPORTANT]
> Automated user accounts or "self-bots" are against Discord's Terms of Service. I am not responsible for any loss caused by using "self-bots" or Discordo.
