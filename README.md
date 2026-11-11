# sunshine-ctl

Turn the physical monitor **off during a Moonlight/Sunshine stream** — and have it
come back automatically when the client disconnects — without interrupting the stream.

Sunshine never runs the `undo` command of `global_prep_cmd` for "Desktop"
sessions, so the usual `ddcutil` tricks either leave the panel on, or cut the
DP link and kill the stream. `sunshine-ctl` takes a different route: the desktop
lives on a **virtual output**, and the physical monitor is a **mirror** of it.
During a stream only the physical output is disabled; the capture source keeps
running.

```
            ┌──────────────────────────┐
            │  virtual output (DP-1)   │  ← Sunshine captures this
            │  desktop lives here      │
            └───────────┬──────────────┘
                        │ mirror
            ┌───────────▼──────────────┐
            │  physical monitor (DP-2) │  ← disabled while streaming
            └──────────────────────────┘
```

## Features

- `watch` daemon: reacts to `CLIENT CONNECTED` / `CLIENT DISCONNECTED` in
  `sunshine.log`, disables/enables the physical output with a 4 s debounce.
- Periodic reconcile (every 10 s): restores the mirror if the monitor was
  powered on again, restores mode/scale if the compositor dropped them.
- No DDC: works even when the monitor does not support MCCS power modes.
- Single static Go binary, no runtime dependencies besides `cosmic-randr`.

## Requirements

- Linux with DRM/KMS and a compositor that ships `cosmic-randr` (COSMIC).
  The idea is portable; `cosmic-randr` calls can be swapped for your
  compositor's CLI.
- Sunshine with `capture = kms` and `output_name` pointing at the virtual output.
- One physical output + one **unused** connector for the virtual one.
- Passwordless `sudo` for the force-probe (see below).

## 1. Create the virtual output (EDID override)

The virtual output is a spare connector forced on with a custom EDID.
Any sane EDID works; the simplest is a copy of your monitor's.

```bash
sudo mkdir -p /usr/lib/firmware/edid
sudo sh -c 'cat /sys/class/drm/card1-DP-2/edid > /usr/lib/firmware/edid/monitor.bin'
```

`/etc/mkinitcpio.conf.d/20-edid.conf`:

```
FILES+=(/usr/lib/firmware/edid/monitor.bin)
```

Kernel command line (e.g. `/etc/default/limine`):

```
drm.edid_firmware=DP-1:edid/monitor.bin video=DP-1:e
```

Rebuild your initramfs and reboot:

```bash
sudo limine-update     # or: sudo mkinitcpio -P
```

After reboot `DP-1` must be `connected` even though nothing is plugged in:

```bash
cat /sys/class/drm/card1-DP-1/status   # connected
```

## 2. Sunshine

`~/.config/sunshine/sunshine.conf`:

```
capture = kms
output_name = DP-1
```

## 3. sudoers (for waking a sleeping monitor)

When the monitor drops off the bus, the kernel needs a forced probe:

```bash
echo detect > /sys/class/drm/card1-DP-2/status
```

Allow it without a password (adjust user and output):

```
youruser ALL=(root) NOPASSWD: /bin/sh -c echo detect > /sys/class/drm/card1-DP-2/status
```

## 4. Install

```bash
git clone https://github.com/FuzzyHD1993/sunshine-ctl
cd sunshine-ctl
./install.sh
```

The installer builds the binary into `~/.local/bin/sunshine-ctl` and enables
`sunshine-ctl.service` as a user unit.

## Usage

```bash
sunshine-ctl status          # client, outputs, mode, scale
sunshine-ctl layout          # virtual primary + physical mirror
sunshine-ctl monitor on|off  # physical monitor
sunshine-ctl stream on|off   # manual stream mode
sunshine-ctl watch           # daemon (systemd runs this)
```

## Configuration

| Variable | Default | Meaning |
|---|---|---|
| `SUNSHINE_CTL_VIRT` | `DP-1` | virtual output (capture source) |
| `SUNSHINE_CTL_PHYS` | `DP-2` | physical output (mirror) |
| `SUNSHINE_CTL_WIDTH` | `2560` | width |
| `SUNSHINE_CTL_HEIGHT` | `1440` | height |
| `SUNSHINE_CTL_REFRESH` | `239958` | refresh in mHz (`239.958 Hz`) |
| `SUNSHINE_CTL_LOG` | `~/.config/sunshine/sunshine.log` | Sunshine log |
| `SUNSHINE_CTL_DEBOUNCE_SEC` | `4` | disconnect debounce |

## Limitations

- A monitor switched off **by its power button** cannot be brought back by
  software. Standby (no signal) is fine.
- Mirroring means the physical monitor always shows the virtual output; the
  virtual deck is the one Sunshine captures.
- Tested with COSMIC + NVIDIA + Sunshine 2026.x.

## License

MIT
