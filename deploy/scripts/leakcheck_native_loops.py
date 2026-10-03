#!/usr/bin/env python3
"""ADR-459: inspect native loop ownership through the Linux loop_info64 ABI."""

import errno
import fcntl
import os
from pathlib import Path
import sys


def main():
    failed = False

    try:
        devices = sorted(Path("/sys/block").iterdir())
    except OSError as error:
        print(f"LEAK: cannot inspect kernel loop inventory: {error}")
        return 1
    # linux/loop.h: LOOP_GET_STATUS64=0x4c05; loop_info64 is 232 bytes,
    # with the 64-byte lo_file_name at offset 56 (fixed-width uint64 ABI).
    for device in devices:
        if not device.name.startswith("loop"):
            continue
        descriptor = None
        try:
            suffix = device.name[4:]
            if not suffix.isdecimal() or str(int(suffix)) != suffix:
                raise ValueError(f"invalid loop name {device.name}")
            descriptor = os.open(f"/dev/{device.name}", os.O_RDONLY | os.O_CLOEXEC | os.O_NOFOLLOW)
            info = bytearray(232)
            fcntl.ioctl(descriptor, 0x4C05, info, True)
            if info[56:120].startswith(b"gregale-loop:"):
                print(f"LEAK: native loop attachment {device.name}")
                failed = True
        except OSError as error:
            if error.errno != errno.ENXIO:
                print(f"LEAK: cannot inspect native loop {device.name}: {error}")
                failed = True
        except ValueError as error:
            print(f"LEAK: cannot inspect native loops: {error}")
            failed = True
        finally:
            if descriptor is not None:
                os.close(descriptor)
    return int(failed)


if __name__ == "__main__":
    sys.exit(main())
