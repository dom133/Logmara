"""Parser for UniFi syslog lines."""

from __future__ import annotations

import re
from dataclasses import dataclass, field
from datetime import datetime
from typing import Optional

import pytz

__all__ = ["UniFiLogEntry", "parse_unifi_log"]

_KV_RE = re.compile(r"(\w+)=([^=]*?)(?=\s+\w+=|$)", re.DOTALL)


@dataclass
class UniFiLogEntry:
    """Parsed UniFi syslog entry."""

    sequence: int
    vendor: str
    product: str
    device_id: str
    port: int
    event: str
    severity: int
    category: Optional[str] = None
    host: Optional[str] = None
    access_method: Optional[str] = None
    admin: Optional[str] = None
    src_ip: Optional[str] = None
    utc_time: Optional[datetime] = None
    message: Optional[str] = None
    raw_kv: dict[str, str] = field(default_factory=dict)

    @property
    def local_time(self) -> Optional[datetime]:
        if self.utc_time is None:
            return None
        return self.utc_time.astimezone(pytz.timezone("Europe/Warsaw"))


def parse_unifi_log(line: str) -> UniFiLogEntry:
    """Parse a single UniFi syslog line.

    Format:
        seq|vendor|product|device_id|port|event|severity|kv_pairs

    Example:
        0|Ubiquiti|UniFi Network|10.4.57|544|Network Accessed|4|UNIFIcategory=Audit ...
    """
    parts = line.split("|", 7)
    if len(parts) < 8:
        raise ValueError(f"Incomplete line (expected 8 fields, got {len(parts)}): {line!r}")

    sequence, vendor, product, device_id, port, event, severity, kv_raw = parts

    kv: dict[str, str] = {}
    for m in _KV_RE.finditer(kv_raw):
        kv[m.group(1)] = m.group(2).strip()

    utc_str = kv.get("UNIFIutcTime", "")
    utc_time: Optional[datetime] = None
    if utc_str:
        try:
            utc_time = datetime.fromisoformat(utc_str.replace("Z", "+00:00"))
        except ValueError:
            pass

    return UniFiLogEntry(
        sequence=int(sequence),
        vendor=vendor.strip(),
        product=product.strip(),
        device_id=device_id.strip(),
        port=int(port),
        event=event.strip(),
        severity=int(severity),
        category=kv.get("UNIFIcategory"),
        host=kv.get("UNIFIhost"),
        access_method=kv.get("UNIFIaccessMethod"),
        admin=kv.get("UNIFIadmin"),
        src_ip=kv.get("src"),
        utc_time=utc_time,
        message=kv.get("msg"),
        raw_kv=kv,
    )