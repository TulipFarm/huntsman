#!/bin/sh
# Huntsman command for native installs: runs the server and manages it as a login service (launchd on macOS,
# systemd --user on Linux, a background process elsewhere). Installed by https://huntsman.tulipfarm.ai/install.sh.
set -eu

home=${HUNTSMAN_HOME:-$HOME/.huntsman}
label=ai.tulipfarm.huntsman
plist=$HOME/Library/LaunchAgents/$label.plist
unit_dir=${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user
unit=$unit_dir/huntsman.service
log=$home/huntsman.log
pidfile=$home/huntsman.pid

usage() {
  cat <<USAGE
Usage: huntsman <command>

  start      Start Huntsman now and at every login
  stop       Stop Huntsman and don't start it at login
  restart    Restart Huntsman, for example after editing $home/env
  status     Show whether Huntsman is running and its MCP URL
  logs       Follow the log
  run        Run in the foreground
  update     Install the latest release
  uninstall  Stop Huntsman and remove it
  version    Print the version

Settings live in $home/env.
USAGE
}

load_env() {
  export HUNTSMAN_DB="$home/data/huntsman.db"
  export HUNTSMAN_BROWSER="$home/libexec/lightpanda"
  lists=""
  for f in "$home"/filters/*.txt; do
    [ -f "$f" ] && lists="${lists:+$lists,}$f"
  done
  export HUNTSMAN_ADBLOCK_LISTS="$lists"
  if [ -f "$home/env" ]; then
    set -a
    # shellcheck disable=SC1091
    . "$home/env"
    set +a
  fi
  addr=${HUNTSMAN_ADDR:-127.0.0.1:8080}
  port=${addr##*:}
  host=${addr%:*}
  case "$host" in "" | 0.0.0.0 | "[::]") host=127.0.0.1 ;; esac
  url="http://$host:$port"
}

service_kind() {
  case "$(uname -s)" in
    Darwin) echo launchd ;;
    *)
      if command -v systemctl >/dev/null 2>&1 && systemctl --user show-environment >/dev/null 2>&1; then
        echo systemd
      else
        echo process
      fi
      ;;
  esac
}

healthy() {
  curl -fsS -m 2 "$url/health" >/dev/null 2>&1
}

wait_healthy() {
  i=0
  while [ $i -lt 30 ]; do
    healthy && return 0
    sleep 0.5
    i=$((i + 1))
  done
  echo "Huntsman did not answer on $url. Last log lines:" >&2
  tail -n 20 "$log" >&2 2>/dev/null || true
  return 1
}

# Keep the log from growing without bound: start fresh once it passes 10 MB.
trim_log() {
  if [ -f "$log" ] && [ "$(wc -c <"$log")" -gt 10485760 ]; then
    mv "$log" "$log.1"
  fi
}

process_running() {
  [ -f "$pidfile" ] && kill -0 "$(cat "$pidfile")" 2>/dev/null
}

cmd_start() {
  load_env
  mkdir -p "$home/data"
  trim_log
  case "$(service_kind)" in
    launchd)
      mkdir -p "$(dirname "$plist")"
      cat >"$plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>$label</string>
  <key>ProgramArguments</key><array><string>$home/bin/huntsman</string><string>run</string></array>
  <key>EnvironmentVariables</key><dict><key>HUNTSMAN_HOME</key><string>$home</string></dict>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict>
  <key>ThrottleInterval</key><integer>5</integer>
  <key>StandardOutPath</key><string>$log</string>
  <key>StandardErrorPath</key><string>$log</string>
</dict>
</plist>
PLIST
      uid=$(id -u)
      if launchctl print "gui/$uid/$label" >/dev/null 2>&1; then
        launchctl kickstart "gui/$uid/$label"
      else
        launchctl bootstrap "gui/$uid" "$plist"
      fi
      ;;
    systemd)
      mkdir -p "$unit_dir"
      cat >"$unit" <<UNIT
[Unit]
Description=Huntsman web scraper
After=network-online.target

[Service]
Environment=HUNTSMAN_HOME=$home
ExecStart=$home/bin/huntsman run
Restart=on-failure
RestartSec=5
StandardOutput=append:$log
StandardError=append:$log

[Install]
WantedBy=default.target
UNIT
      systemctl --user daemon-reload
      systemctl --user enable --now huntsman.service >/dev/null
      if [ "$(loginctl show-user "$(id -un)" --property=Linger --value 2>/dev/null)" = no ]; then
        echo "To keep Huntsman running after you log out (for example over SSH), run: loginctl enable-linger" >&2
      fi
      ;;
    process)
      if ! process_running; then
        nohup "$home/bin/huntsman" run >>"$log" 2>&1 &
        echo $! >"$pidfile"
      fi
      echo "No login service manager found, so Huntsman runs until you log out or reboot." >&2
      ;;
  esac
  wait_healthy
  echo "Huntsman is running at $url (MCP: $url/mcp)"
}

cmd_stop() {
  case "$(service_kind)" in
    launchd)
      launchctl bootout "gui/$(id -u)/$label" 2>/dev/null || true
      rm -f "$plist"
      ;;
    systemd)
      if [ -f "$unit" ]; then
        systemctl --user disable --now huntsman.service >/dev/null 2>&1 || true
        rm -f "$unit"
        systemctl --user daemon-reload
      fi
      ;;
    process)
      if process_running; then
        kill "$(cat "$pidfile")"
      fi
      rm -f "$pidfile"
      ;;
  esac
  echo "Huntsman stopped."
}

cmd_status() {
  load_env
  echo "version  $("$home/libexec/huntsman" version)"
  echo "service  $(service_kind)"
  if healthy; then
    echo "status   running"
    echo "api      $url/v1/scrape"
    echo "mcp      $url/mcp"
  else
    echo "status   not running (run 'huntsman start')"
    return 1
  fi
}

cmd_uninstall() {
  cmd_stop >/dev/null
  bin=${XDG_BIN_HOME:-$HOME/.local/bin}/huntsman
  if [ -L "$bin" ] && [ "$(readlink "$bin")" = "$home/bin/huntsman" ]; then
    rm -f "$bin"
  fi
  rm -rf "$home"
  echo "Huntsman removed. Remove the huntsman MCP entry from your agents' settings too."
}

case "${1:-help}" in
  start) cmd_start ;;
  stop) cmd_stop ;;
  restart)
    cmd_stop >/dev/null
    cmd_start
    ;;
  status) cmd_status ;;
  logs) tail -n 50 -f "$log" ;;
  run)
    load_env
    exec "$home/libexec/huntsman"
    ;;
  update) curl -fsSL https://huntsman.tulipfarm.ai/install.sh | sh ;;
  uninstall) cmd_uninstall ;;
  version) exec "$home/libexec/huntsman" version ;;
  help | -h | --help) usage ;;
  *)
    usage >&2
    exit 2
    ;;
esac
