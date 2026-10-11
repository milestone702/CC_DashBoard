import tarfile, gzip, io, os, sys

root = r"E:\Coding\CC_DashBoard\build"

def ar_members(path):
    raw = open(path, "rb").read()
    assert raw[:8] == b"!<arch>\n", path + " not ar"
    off, out = 8, {}
    while off + 60 <= len(raw):
        hdr = raw[off:off+60]
        name = hdr[:16].decode("ascii", "replace").strip().rstrip("/")
        size = int(hdr[48:58].decode().strip())
        out[name] = raw[off+60:off+60+size]
        off += 60 + size + (size % 2)
    return out

def check_deb(path, bin_name, unit_name, expect_script=True):
    m = ar_members(path)
    ctrl = tarfile.open(fileobj=io.BytesIO(gzip.decompress(m["control.tar.gz"])))
    data = tarfile.open(fileobj=io.BytesIO(gzip.decompress(m["data.tar.gz"])))
    ctrl_names = ctrl.getnames()
    data_names = data.getnames()
    print(f"== {os.path.basename(path)} ==")
    print("  control:", ctrl_names)
    ok = True
    # 二进制
    bin_member = [n for n in data_names if (n.endswith("/" + bin_name) or n.endswith(bin_name)) and not data.getmember(n).isdir()]
    if not bin_member:
        print(f"  MISSING binary {bin_name}"); ok = False
    else:
        f = data.extractfile(bin_member[0])
        head = f.read(4)
        if head != b"\x7fELF":
            print(f"  binary not ELF: {head}"); ok = False
        else:
            print(f"  binary {bin_name}: ELF ok ({data.getmember(bin_member[0]).size} bytes)")
    # systemd unit
    unit_member = [n for n in data_names if n.endswith(unit_name)]
    if not unit_member:
        print(f"  MISSING unit {unit_name}"); ok = False
    else:
        unit = data.extractfile(unit_member[0]).read().decode()
        if "WantedBy=multi-user.target" not in unit:
            print("  unit missing WantedBy"); ok = False
        else:
            print(f"  unit {unit_name}: ok")
    # postinst
    if expect_script:
        pi = [n for n in ctrl_names if n.endswith("postinst") or n.endswith("postinstall")]
        if not pi:
            print("  MISSING postinst"); ok = False
        else:
            body = ctrl.extractfile(pi[0]).read().decode()
            if "systemctl enable" not in body:
                print("  postinst missing systemctl enable"); ok = False
            else:
                print(f"  postinst ({pi[0]}): contains systemctl enable")
    print("  =>", "PASS" if ok else "FAIL")
    return ok

r1 = check_deb(os.path.join(root, "cc-agent_1.1.0_amd64.deb"), "cc-agent", "cc-agent.service")
r2 = check_deb(os.path.join(root, "cc-dashboard_1.1.0_amd64.deb"), "cc-dashboard", "cc-dashboard.service")
# config example
m = ar_members(os.path.join(root, "cc-agent_1.1.0_amd64.deb"))
data = tarfile.open(fileobj=io.BytesIO(gzip.decompress(m["data.tar.gz"])))
ex = [n for n in data.getnames() if "config.example" in n]
if ex:
    print("config example:", data.extractfile(ex[0]).read().decode()[:80], "...")
else:
    print("MISSING config example"); r1 = False

sys.exit(0 if (r1 and r2) else 1)
