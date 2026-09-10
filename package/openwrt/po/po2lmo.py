#!/usr/bin/env python3
# po2lmo.py - 纯 Python 的 po -> lmo 转换器，与 LuCI 的 po2lmo.c 输出逐字节一致。
# 用于免 SDK 的 ipk 打包（scripts/build-release.sh），构建主机只需 python3。
# 用法: po2lmo.py input.po output.lmo
# 说明: LMO 是 LuCI 专有的翻译格式（docs/LMO.md），msgfmt 生成的 GNU MO 不兼容。

import os
import struct
import sys

MASK = 0xFFFFFFFF


def _s8(b):
    return b - 256 if b >= 128 else b


def sfh_hash(data, init):
    # SuperFastHash，逐字节复刻 luci-base/src/lib/lmo.c（含 signed char 语义）
    if not data:
        return 0
    h = init & MASK
    n, rem = divmod(len(data), 4)

    def get16(o):
        return data[o] | (data[o + 1] << 8)

    i = 0
    for _ in range(n):
        h = (h + get16(i)) & MASK
        tmp = ((get16(i + 2) << 11) ^ h) & MASK
        h = ((h << 16) ^ tmp) & MASK
        i += 4
        h = (h + (h >> 11)) & MASK

    if rem == 3:
        h = (h + get16(i)) & MASK
        h = (h ^ (h << 16)) & MASK
        h = (h ^ (_s8(data[i + 2]) << 18)) & MASK
        h = (h + (h >> 11)) & MASK
    elif rem == 2:
        h = (h + get16(i)) & MASK
        h = (h ^ (h << 11)) & MASK
        h = (h + (h >> 17)) & MASK
    elif rem == 1:
        h = (h + _s8(data[i])) & MASK
        h = (h ^ (h << 10)) & MASK
        h = (h + (h >> 1)) & MASK

    h = (h ^ (h << 3)) & MASK
    h = (h + (h >> 5)) & MASK
    h = (h ^ (h << 4)) & MASK
    h = (h + (h >> 17)) & MASK
    h = (h ^ (h << 25)) & MASK
    h = (h + (h >> 6)) & MASK
    return h


def extract_string(line):
    # 复刻 po2lmo.c extract_string：只反转义 \" 和 \\，其余原样保留；
    # 空字符串返回 None（对应 C 中字段保持 NULL）
    if line.startswith(b'#'):
        return None
    out = bytearray()
    off = -1
    esc = False
    for pos, ch in enumerate(line):
        if off == -1:
            if ch == 0x22:  # '"'
                off = pos + 1
            continue
        if esc:
            if ch in (0x22, 0x5C):  # '"' or '\\'
                if out:
                    out.pop()
            out.append(ch)
            esc = False
        elif ch == 0x5C:
            out.append(ch)
            esc = True
        elif ch != 0x22:
            out.append(ch)
        else:
            break
    if off < 0 or len(out) == 0:
        return None
    return bytes(out)


class Msg:
    def __init__(self):
        self.plural_num = -1
        self.ctxt = None
        self.id = None
        self.id_plural = None
        self.val = [None] * 10
        self.cur = None


def write_entry_data(out, value, offset):
    out += value
    pad = (4 - (len(value) % 4)) % 4
    out += b'\0' * pad
    return offset + len(value) + pad


def convert(po_path, lmo_path):
    entries = []
    offset = 0
    data = bytearray()

    def flush(msg):
        nonlocal offset
        if msg.id is not None and msg.val[0] is not None:
            for i in range(msg.plural_num + 1):
                val = msg.val[i]
                if val is None:
                    continue
                if msg.ctxt is not None and msg.id_plural is not None:
                    key = msg.ctxt + b'\x01' + msg.id + b'\x02' + str(i).encode()
                elif msg.ctxt is not None:
                    key = msg.ctxt + b'\x01' + msg.id
                elif msg.id_plural is not None:
                    key = msg.id + b'\x02' + str(i).encode()
                else:
                    key = msg.id
                key_id = sfh_hash(key, len(key))
                if key_id != sfh_hash(val, len(val)):
                    entries.append((key_id, msg.plural_num + 1, offset, len(val)))
                    offset = write_entry_data(data, val, offset)
        elif msg.val[0] is not None:
            # 头部条目（msgid ""）：提取 Plural-Forms，存到 key_id 0
            field_start = 0
            raw = msg.val[0]
            esc = False
            for p in range(len(raw)):
                ch = raw[p]
                if esc:
                    if ch == 0x6E:  # 'n'
                        field = raw[field_start:p - 1]
                        if field[:14].lower() == b'plural-forms: ':
                            val = field[14:]
                            entries.append((0, 0, offset, len(val)))
                            offset = write_entry_data(data, val, offset)
                            break
                        field_start = p + 1
                    esc = False
                elif ch == 0x5C:
                    esc = True

    msg = Msg()
    cur = None
    with open(po_path, 'rb') as f:
        lines = f.read().split(b'\n')
    if lines and lines[-1] == b'':
        lines.pop()

    for line in lines:
        line = line.rstrip(b'\r')

        if line.startswith(b'msgctxt "'):
            if msg.id is not None or msg.val[0] is not None:
                flush(msg)
                msg = Msg()
            msg.ctxt = None
            cur = 'ctxt'
        elif line.startswith(b'msgid "'):
            if msg.id is not None or msg.val[0] is not None:
                flush(msg)
                msg = Msg()
            msg.id = None
            cur = 'id'
        elif line.startswith(b'msgid_plural "'):
            msg.id_plural = None
            cur = 'id_plural'
        elif line.startswith(b'msgstr "') or line.startswith(b'msgstr['):
            if line[6:7] == b'[':
                msg.plural_num = int(line[7:].split(b']')[0])
                if msg.plural_num >= 10:
                    sys.exit('Error: Too many plural forms')
            else:
                msg.plural_num = 0
            msg.val[msg.plural_num] = None
            cur = ('val', msg.plural_num)
        if cur is not None:
            s = extract_string(line)
            if s is not None:
                if isinstance(cur, tuple):
                    msg.val[cur[1]] = (msg.val[cur[1]] or b'') + s
                else:
                    setattr(msg, cur, (getattr(msg, cur) or b'') + s)

    if msg.id is not None or msg.val[0] is not None:
        flush(msg)

    entries.sort(key=lambda e: e[0])
    if offset > 0:
        with open(lmo_path, 'wb') as out:
            out.write(bytes(data))
            for key_id, val_id, off, length in entries:
                out.write(struct.pack('>4I', key_id, val_id, off, length))
            out.write(struct.pack('>I', offset))
    elif os.path.exists(lmo_path):
        os.unlink(lmo_path)


def main():
    if len(sys.argv) != 3:
        sys.exit('Usage: %s input.po output.lmo' % sys.argv[0])
    convert(sys.argv[1], sys.argv[2])


if __name__ == '__main__':
    main()
