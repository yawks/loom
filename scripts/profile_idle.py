#!/usr/bin/env python3
"""macOS idle measurement: python3 scripts/profile_idle.py --restart.

Build Loom first. Leaves the diagnostic application running; never installs it.
Profiles contain stack names only; bridge logs contain aggregate metrics only.
"""
import argparse
import datetime
import json
import os
import pathlib
import re
import select
import statistics
import subprocess
import time


def processes():
    output = subprocess.check_output(["ps", "-axo", "pid=,command="], text=True)
    return {int(line.split(None, 1)[0]): line.split(None, 1)[1] for line in output.splitlines() if len(line.split(None, 1)) == 2}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--restart", action="store_true")
    parser.add_argument("--duration", type=int, default=120)
    parser.add_argument("--delay", type=int, default=120)
    parser.add_argument("--max-wait", type=int, default=0, help="Explicitly allow a non-idle capture after this many seconds of waiting (0: strict idle only)")
    parser.add_argument("--label", default="idle")
    parser.add_argument("--output", required=True)
    args = parser.parse_args()
    # A sleep/wake both stretches a capture and starts catch-up sync, making
    # before/after comparisons invalid. Hold an idle-sleep assertion only while
    # this measurement process exists; do not keep the display awake.
    assertion = subprocess.Popen(["caffeinate", "-i", "-w", str(os.getpid())])
    import atexit
    atexit.register(assertion.terminate)
    root = pathlib.Path(__file__).resolve().parent.parent
    binary = root / "build/bin/Loom.app/Contents/MacOS/Loom"
    if args.duration <= 0 or args.delay < 0 or args.max_wait < 0:
        parser.error("invalid duration/delay")
    if args.restart:
        subprocess.run(["osascript", "-e", 'tell application id "com.wails.Loom" to quit'], capture_output=True)
    before = processes()
    if any("/Loom.app/Contents/MacOS/Loom" in cmd for cmd in before.values()):
        raise SystemExit("Another Loom instance is running; quit it before measuring.")
    stamp = datetime.datetime.now().strftime("%Y%m%d-%H%M%S")
    output = pathlib.Path(args.output).resolve() / f"{args.label}-{stamp}"
    output.mkdir(mode=0o700, parents=True)
    profile = output / "cpu.pprof"
    fd = os.open(output / "application.log", os.O_CREAT | os.O_EXCL | os.O_RDWR, 0o600)
    env = dict(os.environ, LOOM_CPU_PROFILE=str(profile), LOOM_CPU_PROFILE_DELAY=f"{args.delay}s", LOOM_CPU_PROFILE_DURATION=f"{args.duration}s", LOOM_CPU_PROFILE_MAX_WAIT=f"{args.max_wait}s", LOOM_BRIDGE_DIAGNOSTICS="1")
    app = subprocess.Popen([str(binary)], env=env, stdout=fd, stderr=fd, start_new_session=True, cwd=root)
    print(f"PID={app.pid} output={output}", flush=True)
    queue = select.kqueue()
    queue.control([select.kevent(fd, filter=select.KQ_FILTER_VNODE, flags=select.KQ_EV_ADD | select.KQ_EV_CLEAR, fflags=select.KQ_NOTE_WRITE)], 0, 0)
    offset, pending = 0, b""
    cpu_saved = metrics_saved = False
    top = None
    targets = []
    metrics = []
    quality = {"strictIdle": True, "cpuComparable": True}
    recording_wall = None
    saved_at = None
    deadline = time.monotonic() + 1200
    while time.monotonic() < deadline:
        queue.control(None, 1, 10)
        data = os.pread(fd, 2**24, offset)
        offset += len(data)
        lines = (pending + data).split(b"\n")
        pending = lines.pop()
        for raw in lines:
            line = raw.decode(errors="replace")
            if "[CPUProfile]" in line or "[BridgeDiagnostics]" in line:
                print(line, flush=True)
            if "not strict idle" in line:
                quality["strictIdle"] = False
            if "[CPUProfile] Recording" in line:
                recording_wall = datetime.datetime.strptime(line[:19], "%Y/%m/%d %H:%M:%S")
                targets = [app.pid] + [pid for pid, cmd in processes().items() if pid not in before and "com.apple.WebKit.WebContent" in cmd]
                command = ["top", "-l", str(args.duration // 2 + 1), "-s", "2"]
                for pid in targets:
                    command += ["-pid", str(pid)]
                command += ["-stats", "pid,command,cpu,time,threads,state"]
                with open(output / "top.txt", "w") as top_file:
                    top = subprocess.Popen(command, stdout=top_file)
            if "[CPUProfile] Saved" in line:
                cpu_saved = True
                saved_at = time.monotonic()
                if recording_wall:
                    elapsed = (datetime.datetime.strptime(line[:19], "%Y/%m/%d %H:%M:%S") - recording_wall).total_seconds()
                    quality["wallSeconds"] = elapsed
                    quality["cpuComparable"] = args.duration - 1 <= elapsed <= args.duration + 10
            if "[BridgeDiagnostics] {" in line:
                metrics.append(json.loads(line.split("[BridgeDiagnostics] ", 1)[1]))
            if "[BridgeDiagnostics] end" in line:
                metrics_saved = True
        if cpu_saved and (metrics_saved or time.monotonic() - saved_at >= 10):
            break
        if app.poll() is not None:
            raise SystemExit(f"Loom exited ({app.returncode}); see {output}")
    else:
        raise SystemExit(f"Timed out waiting for diagnostic capture; see {output}")
    if top:
        try:
            top.wait(timeout=30)
        except subprocess.TimeoutExpired:
            top.terminate()
            top.wait(timeout=5)
            quality["cpuComparable"] = False
    for label, flags in [("top", []), ("cumulative", ["-cum"])]:
        result = subprocess.run(["go", "tool", "pprof", "-top", *flags, "-nodecount=55", str(binary), str(profile)], capture_output=True, text=True, check=True)
        (output / f"pprof-{label}.txt").write_text(result.stdout + result.stderr)
        print(result.stdout, flush=True)
    metrics.sort(key=lambda item: item["stringChars"], reverse=True)
    (output / "bridge.json").write_text(json.dumps(metrics, indent=2))
    top_text = (output / "top.txt").read_text()
    cpu = {}
    for pid in targets:
        values = [float(v) for v in re.findall(r"^" + str(pid) + r"\s+\S+\s+([\d.]+)", top_text, re.M)][1:]
        if values:
            cpu[str(pid)] = {"mean": statistics.mean(values), "median": statistics.median(values), "max": max(values), "samples": len(values)}
    (output / "cpu.json").write_text(json.dumps(cpu, indent=2))
    quality["bridgeMetricsAvailable"] = metrics_saved
    (output / "quality.json").write_text(json.dumps(quality, indent=2))
    print("OS CPU:", json.dumps(cpu), flush=True)
    print("QUALITY:", json.dumps(quality), flush=True)
    print("RESULT", output, flush=True)
    os.close(fd)


if __name__ == "__main__":
    main()
