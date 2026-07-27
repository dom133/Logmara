#!/usr/bin/env python3
"""
Logmara Log Generator — sends realistic syslog entries to a Logmara server.

Usage:
  python3 generate_logs.py --host 192.168.1.100 --count 500000 --rate 5000
"""

import argparse
import random
import socket
import sys
import threading
import time
from collections import deque
from datetime import datetime, timedelta, timezone

# ---------------------------------------------------------------------------
# Severity / Facility helpers
# ---------------------------------------------------------------------------

SEVERITIES = ["emerg", "alert", "crit", "err", "warning", "notice", "info", "debug"]
FACILITIES = {
    "kern": 0, "user": 1, "mail": 2, "daemon": 3,
    "auth": 4, "syslog": 5, "lpr": 6, "news": 7,
    "uucp": 8, "cron": 9, "authpriv": 10, "ftp": 11,
    "local0": 16, "local1": 17, "local2": 18, "local3": 19,
    "local4": 20, "local5": 21, "local6": 22, "local7": 23,
}

MONTH_MAP = {
    1: "Jan", 2: "Feb", 3: "Mar", 4: "Apr", 5: "May", 6: "Jun",
    7: "Jul", 8: "Aug", 9: "Sep", 10: "Oct", 11: "Nov", 12: "Dec",
}


def pri(facility: str, severity: str) -> int:
    return FACILITIES[facility] * 8 + SEVERITIES.index(severity)


def rfc3164_timestamp(dt: datetime) -> str:
    return f"{MONTH_MAP[dt.month]} {dt.day:02d} {dt.strftime('%H:%M:%S')}"


def make_syslog(dt: datetime, hostname: str, app: str, pid: str,
                facility: str, severity: str, message: str) -> bytes:
    p = pri(facility, severity)
    ts = rfc3164_timestamp(dt)
    line = f"<{p}>{ts} {hostname} {app}[{pid}]: {message}\n"
    return line.encode("utf-8")


# ---------------------------------------------------------------------------
# Realistic message templates
# ---------------------------------------------------------------------------

class DeviceProfile:
    """A device with its hostname, IP, and message generators."""

    def __init__(self, hostname: str, ip: str, app: str, facility: str,
                 pid_range: tuple[int, int]):
        self.hostname = hostname
        self.ip = ip
        self.app = app
        self.facility = facility
        self.pid_range = pid_range
        self.messages: list[tuple[str, str]] = []  # (severity, message)

    def gen(self, rng: random.Random, dt: datetime) -> tuple[str, bytes]:
        sev, msg_template = rng.choice(self.messages)
        pid = str(rng.randint(*self.pid_range))
        msg = msg_template.format(
            ip=rng_ip(rng), user=rng_user(rng), port=rng_port(rng),
            mac=rng_mac(rng), dt=dt.strftime("%Y-%m-%d %H:%M:%S"),
            n=rng.randint(1, 9999), pct=rng.randint(1, 100),
            code=rng.choice(["200", "301", "403", "404", "500", "502", "503"]),
            proto=rng.choice(["TCP", "UDP"]),
            iface=rng.choice(["eth0", "eth1", "wan", "lan", "wlan0", "vlan10"]),
            status=rng.choice(["up", "down", "flapping"]),
            reason=rng.choice(["timeout", "reset", "admin", "link failure", "policy violation"]),
            app=self.app,
        )
        return sev, make_syslog(dt, self.hostname, self.app, pid,
                                self.facility, sev, msg)


def rng_ip(r: random.Random) -> str:
    return f"{r.randint(1,254)}.{r.randint(0,255)}.{r.randint(0,255)}.{r.randint(1,254)}"


def rng_user(r: random.Random) -> str:
    return r.choice([
        "admin", "root", "user1", "deploy", "backup", "www-data",
        "postgres", "mysql", "jenkins", "gitlab-runner", "monitoring",
        "operator", "guest", "test", "service_acct", "svc-backup",
    ])


def rng_port(r: random.Random) -> int:
    return r.choice([22, 80, 443, 8080, 8443, 3306, 5432, 514, 161, 389, 636]) + r.randint(0, 10000)


def rng_mac(r: random.Random) -> str:
    return ":".join(f"{r.randint(0,255):02x}" for _ in range(6))


# ---- Build device fleet ----

def build_fleet() -> list[DeviceProfile]:
    fleet: list[DeviceProfile] = []

    # --- Firewalls (pfSense) ---
    for i in range(1, 6):
        fp = DeviceProfile(f"fw-{i:02d}", f"10.0.0.{i}", "kernel", "kern", (0, 0))
        fp.messages = [
            ("info", "pass quick on {iface}: {proto} {ip}:{port} -> {ip}:{port}"),
            ("warning", "block drop on {iface}: {proto} {ip}:{port} -> 10.0.0.1:{port} ({reason})"),
            ("err", "table overflow: {n} entries exceeded threshold"),
            ("info", "carp: {iface} MASTER -> BACKUP failover"),
            ("crit", "pf: rules reload failed — syntax error at line {n}"),
        ]
        fleet.append(fp)

    # --- Firewalls (Fortigate) ---
    for i in range(6, 8):
        fp = DeviceProfile(f"fgt-{i:02d}", f"10.0.1.{i}", "ftgd", "local0", (1000, 9999))
        fp.messages = [
            ("info", "utm av: virus detected in email from {ip} — subject: 'Invoice #{n}'"),
            ("info", "ips: attack signature 'SQL Injection' from {ip}:{port}"),
            ("warning", "vpn tunnel to {ip} disconnected — {reason}"),
            ("info", "policy hit: rule {n} ALLOW {proto} {ip}:{port} -> {ip}:{port}"),
        ]
        fleet.append(fp)

    # --- Routers (Cisco IOS) ---
    for i in range(1, 4):
        fp = DeviceProfile(f"rtr-{i:02d}", f"10.1.0.{i}", "IOS", "local1", (0, 0))
        fp.messages = [
            ("info", "%LINK-3-UPDOWN: Interface GigabitEthernet0/{n}, line protocol changed state, link up"),
            ("err", "%LINK-3-UPDOWN: Interface GigabitEthernet0/{n}, line protocol changed state, link down"),
            ("warning", "%SECURITY-5-DENY_IP: Deny {proto} from {ip}:{port}"),
            ("info", "%OSPF-5-ADJCHG: Process 1: OSPF neighbor {ip} state Change to FULL"),
            ("info", "%SYS-5-CONFIG_I: Configured from console by {user}"),
        ]
        fleet.append(fp)

    # --- Routers (MikroTik) ---
    for i in range(4, 6):
        fp = DeviceProfile(f"mikrotik-{i:02d}", f"10.1.1.{i}", "routeros", "local1", (0, 0))
        fp.messages = [
            ("info", "dhcp: assigned {ip} to {mac} (lease {n}s)"),
            ("info", "pppoe: client {user} connected from {ip}"),
            ("warning", "interface {iface} link down"),
            ("err", "bgp: peer {ip} session reset — {reason}"),
        ]
        fleet.append(fp)

    # --- Linux servers ---
    linux_hosts = [
        ("web-01", "192.168.1.10", "nginx", "local2", "daemon"),
        ("web-02", "192.168.1.11", "nginx", "local2", "daemon"),
        ("api-01", "192.168.1.20", "node", "local2", "daemon"),
        ("api-02", "192.168.1.21", "java", "local2", "daemon"),
        ("db-01", "192.168.1.30", "postgres", "local2", "daemon"),
        ("db-02", "192.168.1.31", "mysqld", "local2", "daemon"),
        ("cache-01", "192.168.1.40", "redis-server", "local2", "daemon"),
        ("lb-01", "192.168.1.50", "haproxy", "local2", "daemon"),
        ("mail-01", "192.168.1.60", "postfix", "mail", "mail"),
        ("dns-01", "192.168.1.70", "named", "local3", "daemon"),
        ("ci-01", "192.168.1.80", "jenkins", "local4", "daemon"),
        ("monitor-01", "192.168.1.90", "prometheus", "local5", "daemon"),
        ("file-01", "192.168.1.100", "smbd", "local6", "daemon"),
        ("proxy-01", "192.168.1.110", "squid", "local6", "daemon"),
    ]
    for hostname, ip, app, facility, fac_key in linux_hosts:
        dp = DeviceProfile(hostname, ip, app, fac_key, (1000, 65000))
        dp.messages = _linux_messages(app)
        fleet.append(dp)

    # --- Linux auth (sshd) ---
    for i in range(1, 11):
        dp = DeviceProfile(f"srv-{i:02d}", f"192.168.2.{i}", "sshd", "auth", (1000, 65000))
        dp.messages = [
            ("info", "Accepted publickey for {user} from {ip} port {port} ssh2"),
            ("warning", "Failed password for {user} from {ip} port {port} ssh2"),
            ("err", "Connection closed by authenticating user {user} {ip} port {port} [preauth]"),
            ("info", "pam_unix(sshd:session): session opened for user {user}(uid={n})"),
            ("info", "pam_unix(sshd:session): session closed for user {user}"),
            ("notice", "Invalid user {user} from {ip} port {port}"),
            ("err", "Maximum authentication attempts exceeded for {user} from {ip}"),
        ]
        fleet.append(dp)

    # --- Linux kernel ---
    for i in range(1, 6):
        dp = DeviceProfile(f"srv-k{i:02d}", f"192.168.3.{i}", "kernel", "kern", (0, 0))
        dp.messages = [
            ("err", "EXT4-fs error (device sda{n}): ext4_journal_check_start: Detected aborted journal"),
            ("warning", "CPU{n}: Core temperature above threshold, cpu clock throttled"),
            ("info", "eth{n}: renamed from eth0"),
            ("crit", "Out of memory: Killed process {n} ({app}), total-vm:{n}kB"),
            ("err", "segfault at {n} ip 00007f{n} sp 00007ffd{n} error 4"),
            ("info", "[UFW BLOCK] IN={iface} OUT= SRC={ip} DST={ip} LEN={n} PROTO={proto} SPT={port} DPT={port}"),
        ]
        fleet.append(dp)

    # --- Windows servers ---
    win_hosts = [
        ("dc-01", "10.10.0.10", "Microsoft-Windows-Security-Auditing", "auth"),
        ("dc-02", "10.10.0.11", "Microsoft-Windows-Security-Auditing", "auth"),
        ("wsus-01", "10.10.0.20", "WindowsUpdateAgent", "local0"),
        ("adfs-01", "10.10.0.30", "ADFS", "local1"),
        ("iis-01", "10.10.0.40", "IIS-WorkerProcess", "local2"),
        ("iis-02", "10.10.0.41", "IIS-WorkerProcess", "local2"),
        ("exch-01", "10.10.0.50", "MSExchangeTransport", "local3"),
        ("file-win-01", "10.10.0.60", "Microsoft-Windows-SMBServer", "local6"),
        ("print-01", "10.10.0.70", "Microsoft-Windows-PrintService", "local7"),
        ("vs-01", "10.10.0.80", "VMware-VSphere", "local4"),
    ]
    for hostname, ip, app, facility in win_hosts:
        dp = DeviceProfile(hostname, ip, app, facility, (4096, 65000))
        dp.messages = _windows_messages(app)
        fleet.append(dp)

    # --- Access Points (Ubiquiti) ---
    for i in range(1, 6):
        dp = DeviceProfile(f"ap-{i:02d}", f"172.16.0.{i}", "ubnt", "local0", (0, 0))
        dp.messages = [
            ("info", "station {mac} associated on wlan{port} (ssid: Office-{n})"),
            ("info", "station {mac} disassociated from wlan{port}"),
            ("warning", "channel interference detected on wlan{port} — {pct}% utilization"),
            ("info", "roaming: {mac} moved from ap-{n:02d} to ap-{n:02d}"),
        ]
        fleet.append(dp)

    # --- IP Cameras / NVR ---
    for i in range(1, 4):
        dp = DeviceProfile(f"cam-{i:02d}", f"172.16.1.{i}", "hikvision", "local3", (0, 0))
        dp.messages = [
            ("info", "Motion detection triggered on channel {n}"),
            ("info", "Recording started: channel {n}, resolution 1920x1080"),
            ("warning", "Storage capacity: {pct}% used on HDD {n}"),
            ("err", "Network disconnected from NVR {ip}"),
            ("info", "PTZ preset {n} activated"),
        ]
        fleet.append(dp)

    # --- NAS (Synology) ---
    for i in range(1, 3):
        dp = DeviceProfile(f"nas-{i:02d}", f"172.16.2.{i}", "syno", "local4", (1000, 9999))
        dp.messages = [
            ("info", "SMB session opened: user={user} src={ip}"),
            ("info", "Snapshot created for volume1 ({n} GB)"),
            ("warning", "Disk SMART warning on /dev/sda{n}: reallocated sectors = {n}"),
            ("err", "RAID5 degraded: disk {n} offline"),
            ("info", "Package Center: updated {app} to v{n}.{n}.{n}"),
        ]
        fleet.append(dp)

    # --- Printers ---
    for i in range(1, 4):
        dp = DeviceProfile(f"printer-{i:02d}", f"172.16.3.{i}", "hp_mfp", "local5", (0, 0))
        dp.messages = [
            ("info", "Job #{n} completed: {user}, {n} pages, duplex"),
            ("warning", "Toner cartridge low: Cyan {pct}% remaining"),
            ("err", "Paper jam in tray {n} — manual intervention required"),
            ("info", "Firmware update to v{n}.{n} completed"),
            ("warning", "Monthly page count: {n}, approaching limit {n}"),
        ]
        fleet.append(dp)

    # --- Switches (Cisco Catalyst) ---
    for i in range(1, 4):
        dp = DeviceProfile(f"sw-{i:02d}", f"10.2.0.{i}", "CATALYST_SOFTWARE", "local1", (0, 0))
        dp.messages = [
            ("info", "%CDP-5-NBR_CHG: neighbor added switch {ip} on Gi0/{n}"),
            ("warning", "%SPANNGUARDED-5-PORT_ERROR: port-channel {n} err-disabled"),
            ("info", "%STP-5-ROOTGUARD_CONFLICT: received BPDU on Gi0/{n}"),
            ("err", "%PHY-3-ETHERNET_DOWN: Interface Gi0/{n} is down ({reason})"),
        ]
        fleet.append(dp)

    # --- IoT sensors ---
    for i in range(1, 6):
        dp = DeviceProfile(f"sensor-{i:02d}", f"172.16.4.{i}", "iot-agent", "local7", (0, 0))
        dp.messages = [
            ("info", "temperature: {n}.{n}°C (threshold {n}.{n}°C)"),
            ("warning", "humidity: {pct}% (above {pct}% threshold)"),
            ("info", "door sensor {n}: state changed to OPEN"),
            ("info", "door sensor {n}: state changed to CLOSED"),
            ("err", "battery level: {pct}% — critical"),
            ("crit", "smoke detector {n}: ALERT"),
        ]
        fleet.append(dp)

    # --- UPS ---
    for i in range(1, 2):
        dp = DeviceProfile(f"ups-{i:02d}", f"172.16.5.{i}", "nut", "local7", (0, 0))
        dp.messages = [
            ("info", "UPS on line power — battery charge {pct}%"),
            ("warning", "UPS on battery — estimated runtime {n} min"),
            ("crit", "UPS low battery — initiating graceful shutdown"),
            ("info", "Self-test completed: {status}"),
        ]
        fleet.append(dp)

    # --- Load balancer ---
    dp = DeviceProfile("lb-ext-01", "10.0.5.1", "traefik", "local2", (1000, 9999))
    dp.messages = [
        ("info", "GET /api/v1/status 200 {n}ms — client {ip}"),
        ("warning", "health check failed for backend {ip}:{port}"),
        ("err", "upstream {ip}:{port} returned 502 Bad Gateway"),
        ("info", "certificate renewed for *.example.com (expires {dt})"),
    ]
    fleet.append(dp)

    # --- Smart lock / door controller ---
    for i in range(1, 3):
        dp = DeviceProfile(f"lock-{i:02d}", f"172.16.6.{i}", "yale", "local7", (0, 0))
        dp.messages = [
            ("info", "Door unlocked by {user} (keycard #{n})"),
            ("info", "Door locked by {user} (keycard #{n})"),
            ("warning", "Failed unlock attempt by unknown card #{n}"),
            ("err", "Battery low: {pct}%"),
        ]
        fleet.append(dp)

    return fleet


def _linux_messages(app: str) -> list[tuple[str, str]]:
    generic = [
        ("info", "started: {user} executed command /usr/bin/{app}"),
        ("info", "connection from {ip}:{port} accepted"),
        ("warning", "resource limit reached: {pct}% of {n} MB used"),
        ("err", "failed to bind to 0.0.0.0:{port} — Address already in use"),
        ("info", "configuration reloaded successfully"),
        ("debug", "request processed in {n}ms"),
    ]
    if app == "nginx":
        generic.extend([
            ("info", "{code} GET /api/v1/users HTTP/1.1 from {ip}"),
            ("warning", "upstream timed out (110: Connection timed out) connecting to {ip}:{port}"),
            ("err", "open() \"/var/www/html/favicon.ico\" failed (2: No such file or directory)"),
        ])
    elif app == "postgres":
        generic.extend([
            ("info", "authentication successful: user={user} database={n}_db"),
            ("warning", "could not serialize access due to concurrent update"),
            ("err", "checkpoint starting: time"),
            ("info", "autovacuum: processing table 'public.sessions' ({n} dead tuples)"),
        ])
    elif app == "mysqld":
        generic.extend([
            ("info", "Connect user '{user}'@'{ip}'"),
            ("warning", "Aborted connection {n} to user '{user}'"),
            ("err", "InnoDB: page corruption detected in page [{n}]"),
        ])
    elif app == "redis-server":
        generic.extend([
            ("notice", "Background saving started by pid {n}"),
            ("info", "DB saved on disk (RDB)"),
            ("warning", "WARNING: Memory usage exceeded {pct}% of maxmemory"),
        ])
    elif app == "haproxy":
        generic.extend([
            ("info", "Server backend/srv{n} is UP — reason: Layer7 check passed"),
            ("warning", "proxy frontend has no server available!"),
            ("err", "conn_error on server backend/srv{n}: Connection refused"),
        ])
    elif app == "postfix":
        generic.extend([
            ("info", "{n}A1B2C3: client={ip}"),
            ("info", "{n}A1B2C3: to=<{user}@example.com>, relay={ip}:{port}, status=sent"),
            ("warning", "{n}A1B2C3: NOQUEUE: reject: RCPT from {ip}: 554 5.7.1 Relaying denied"),
        ])
    elif app == "named":
        generic.extend([
            ("info", "client {ip}# {port}: query: example.com IN A"),
            ("warning", "zone transfer failed for 'internal.zone' from {ip}: connection timed out"),
        ])
    elif app == "prometheus":
        generic.extend([
            ("info", "scrape complete: target={ip}:{port} duration={n}ms"),
            ("warning", "target {ip}:{port} is DOWN — consecutive failures: {n}"),
        ])
    return generic


def _windows_messages(app: str) -> list[tuple[str, str]]:
    msgs: list[tuple[str, str]] = []
    if "Security" in app:
        msgs = [
            ("info", "EventID 4624: An account was successfully logged on — {user} from {ip}"),
            ("warning", "EventID 4625: Logon failure for {user} from {ip} (reason: {reason})"),
            ("info", "EventID 4634: An account was logged off — {user}"),
            ("err", "EventID 4768: KDC pre-authentication failure for {user}"),
            ("info", "EventID 4720: A user account was created — {user}"),
            ("warning", "EventID 4740: Account locked out — {user} after {n} attempts"),
        ]
    elif "IIS" in app:
        msgs = [
            ("info", "Request: GET /default.aspx — {code} from {ip}"),
            ("err", "Application pool 'DefaultAppPool' recycled — {reason}"),
            ("warning", "Request timeout after {n}s for /api/report"),
        ]
    elif "Exchange" in app:
        msgs = [
            ("info", "Mailbox {user}: delivered message '{n}' to {n} recipients"),
            ("warning", "Queue {n}: delivery delayed to {ip}:{port} — {reason}"),
            ("err", "Transport service failed to connect to {ip}:{port}"),
        ]
    elif "WindowsUpdate" in app:
        msgs = [
            ("info", "Update KB{n} installed successfully"),
            ("warning", "Update KB{n} failed with error 0x{n}"),
        ]
    else:
        msgs = [
            ("info", "Service {app} started (event {n})"),
            ("warning", "Service {app} stopped unexpectedly"),
            ("err", "Disk {n}: SMART predictive failure — {reason}"),
            ("info", "Backup completed: {n} GB in {n} minutes"),
        ]
    return msgs


# ---------------------------------------------------------------------------
# Severity distribution weights
# ---------------------------------------------------------------------------
SEV_WEIGHTS = {
    "info": 40,
    "notice": 10,
    "warning": 20,
    "err": 15,
    "crit": 5,
    "alert": 2,
    "emerg": 1,
    "debug": 7,
}
WEIGHTED_SEVS = []
for sev, w in SEV_WEIGHTS.items():
    WEIGHTED_SEVS.extend([sev] * w)


# ---------------------------------------------------------------------------
# Generator logic
# ---------------------------------------------------------------------------

def generate_logs(fleet: list[DeviceProfile], count: int, hours: float,
                  rng: random.Random, start_time: datetime | None = None):
    """Yield (severity, raw_bytes) tuples."""
    if start_time is None:
        start_time = datetime.now(timezone.utc) - timedelta(hours=hours)
    end_time = start_time + timedelta(hours=hours)
    span_seconds = hours * 3600

    for _ in range(count):
        dt = start_time + timedelta(seconds=rng.uniform(0, span_seconds))
        profile = rng.choice(fleet)
        sev, raw = profile.gen(rng, dt)
        yield sev, raw


# ---------------------------------------------------------------------------
# Network sender
# ---------------------------------------------------------------------------

class Sender:
    def __init__(self, host: str, port: int, protocol: str = "udp"):
        self.host = host
        self.port = port
        self.protocol = protocol
        self.sock: socket.socket | None = None
        self.sent = 0
        self.errors = 0
        self._lock = threading.Lock()

    def connect(self):
        if self.protocol == "udp":
            self.sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
        else:
            self.sock = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        self.sock.connect((self.host, self.port))

    def send(self, data: bytes):
        try:
            self.sock.sendall(data)
            with self._lock:
                self.sent += 1
        except Exception:
            with self._lock:
                self.errors += 1

    def close(self):
        if self.sock:
            self.sock.close()


# ---------------------------------------------------------------------------
# Progress bar
# ---------------------------------------------------------------------------

class ProgressBar:
    def __init__(self, total: int, width: int = 40):
        self.total = total
        self.width = width
        self.current = 0
        self.lock = threading.Lock()

    def update(self, n: int = 1):
        with self.lock:
            self.current += n
            if self.current > self.total:
                self.current = self.total

    def draw(self):
        with self.lock:
            pct = self.current / self.total
            filled = int(self.width * pct)
            bar = "=" * filled + "." * (self.width - filled)
            sys.stdout.write(f"\r[{bar}] {self.current}/{self.total} ({pct*100:.1f}%)")
            sys.stdout.flush()

    def done(self):
        print()


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

def main():
    parser = argparse.ArgumentParser(
        description="Generate and send realistic syslog entries to a Logmara server."
    )
    parser.add_argument("--host", default="localhost", help="Target server IP (default: localhost)")
    parser.add_argument("--port", type=int, default=514, help="Syslog port (default: 514)")
    parser.add_argument("--protocol", choices=["udp", "tcp"], default="udp", help="Protocol (default: udp)")
    parser.add_argument("--count", type=int, default=500_000, help="Number of logs to generate (default: 500000)")
    parser.add_argument("--rate", type=int, default=0, help="Rate limit in logs/sec (0 = unlimited)")
    parser.add_argument("--workers", type=int, default=4, help="Number of sender threads (default: 4)")
    parser.add_argument("--hours", type=float, default=24, help="Time span in hours (default: 24)")
    parser.add_argument("--seed", type=int, default=None, help="Random seed for reproducibility")
    args = parser.parse_args()

    rng = random.Random(args.seed)
    fleet = build_fleet()

    print(f"Target: {args.host}:{args.port} ({args.protocol.upper()})")
    print(f"Count: {args.count:,} | Rate: {'unlimited' if args.rate == 0 else args.rate}/s | Workers: {args.workers}")
    print(f"Devices: {len(fleet)} | Time span: {args.hours}h")
    print()

    # Pre-generate all logs into batches for thread safety
    gen = generate_logs(fleet, args.count, args.hours, rng)
    batches: deque[list[bytes]] = deque()
    batch_size = max(100, args.count // (args.workers * 100))

    batch: list[bytes] = []
    for sev, raw in gen:
        batch.append(raw)
        if len(batch) >= batch_size:
            batches.append(batch)
            batch = []
    if batch:
        batches.append(batch)

    total_batches = len(batches)
    print(f"Batches: {total_batches} (avg {batch_size} logs/batch)")
    print()

    progress = ProgressBar(args.count)
    start_ts = time.time()

    senders = [Sender(args.host, args.port, args.protocol) for _ in range(args.workers)]
    for s in senders:
        s.connect()

    def worker(sender: Sender):
        while True:
            try:
                batch = batches.popleft()
            except IndexError:
                break
            for msg in batch:
                sender.send(msg)
                if args.rate > 0:
                    time.sleep(1.0 / args.rate)
            progress.update(len(batch))
            progress.draw()

    threads = []
    for sender in senders:
        t = threading.Thread(target=worker, args=(sender,), daemon=True)
        t.start()
        threads.append(t)

    for t in threads:
        t.join()

    elapsed = time.time() - start_ts
    progress.done()

    total_sent = sum(s.sent for s in senders)
    total_err = sum(s.errors for s in senders)

    for s in senders:
        s.close()

    print(f"\nDone!")
    print(f"  Sent:     {total_sent:,} logs")
    print(f"  Errors:   {total_err:,}")
    print(f"  Time:     {elapsed:.1f}s")
    print(f"  Speed:    {total_sent / elapsed:,.0f} logs/sec")
    if args.seed is not None:
        print(f"  Seed:     {args.seed}")


if __name__ == "__main__":
    main()