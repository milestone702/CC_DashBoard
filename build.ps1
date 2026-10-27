# CC_DashBoard 一键构建脚本
# 用法: powershell -ExecutionPolicy Bypass -File build.ps1 [-Version V1.0] [-Auto]
param(
    [string]$Version = "",   # 直接指定本次版本号（如 -Version V2.0），跳过交互
    [switch]$Auto            # 非交互：自动 +0.1（CI / 脚本场景）
)
# 产物: build/ 目录（按架构分文件夹的裸二进制 + ipk/deb/rpm + fnos/*.fpk + synology/*.spk）
$ErrorActionPreference = "Stop"
Set-Location $PSScriptRoot

# ---- 版本号：记录于 VERSION 文件，每次构建交互提示（回车 = 自动 +0.1）----
# 指定/变更版本三种方式：1) 构建时输入（如 V2.0 升大版本）；2) -Version V2.0 参数；3) 直接改 VERSION 文件。
# 打包（deb/rpm/ipk）要求版本号数字开头，另备 $pkgVersion（去掉 V 前缀）。
$verFile = Join-Path $PSScriptRoot 'VERSION'
$lastVer = ''
if (Test-Path $verFile) {
    $lastVer = (Get-Content $verFile -TotalCount 1 -ErrorAction SilentlyContinue | Select-Object -First 1)
    if ($lastVer) { $lastVer = $lastVer.Trim() }
}
if ($lastVer -notmatch '^V?\d') {
    # 首次（无 VERSION 文件）：以 git 提交数作种子 V0.{提交数}.0，无 git 则 V0.1.0
    $seed = 1
    try {
        $c = git rev-list --count HEAD 2>$null
        if ($LASTEXITCODE -eq 0 -and $c) { $seed = [int]$c.Trim() }
    } catch { }
    $lastVer = "V0.$seed.0"
}
# 版本号统一三段式：旧文件里的 V0.2 / V2 读取时补足为 V0.2.0 / V2.0.0，随后写回也是三段
if ($lastVer -match '^[vV]?(\d+)(?:\.(\d+))?(?:\.(\d+))?$') {
    $vmaj = [int]$Matches[1]
    $vmin = 0; $vpat = 0
    if (-not [string]::IsNullOrEmpty($Matches[2])) { $vmin = [int]$Matches[2] }
    if (-not [string]::IsNullOrEmpty($Matches[3])) { $vpat = [int]$Matches[3] }
    $lastVer = "V$vmaj.$vmin.$vpat"
}
function Get-NextPatch([string]$v) {
    # 三段式：次版本 +1、补丁归零，如 V0.2.0 → V0.3.0（每次构建升一个次版本）
    if ($v -match '^V?(\d+)\.(\d+)\.(\d+)$') {
        return ('V{0}.{1}.0' -f [int]$Matches[1], ([int]$Matches[2] + 1))
    }
    # 旧的两段写法（V0.2）：视为 V0.2.0 后按同样规则递增 → V0.3.0
    if ($v -match '^V?(\d+)\.(\d+)$') { return ('V{0}.{1}.0' -f [int]$Matches[1], ([int]$Matches[2] + 1)) }
    if ($v -match '^V?(\d+)$') { return ('V{0}.0.0' -f ([int]$Matches[1] + 1)) }
    return $v
}
$defaultNext = Get-NextPatch $lastVer
# 无控制台输入（重定向/CI）且未显式指定时自动降级，避免 Read-Host 挂起
if (-not $Version -and -not $Auto -and [Console]::IsInputRedirected) { $Auto = $true }
if ($Version) {
    if ($Version -notmatch '^[vV]?\d+(\.\d+){0,2}$') { throw "版本号格式无效: $Version（示例 V1.0 / V2 / V2.3.1）" }
    $version = 'V' + ($Version -replace '^[vV]', '')
} elseif ($Auto) {
    $version = $defaultNext
} else {
    Write-Host "[0/6] 上次版本: $lastVer"
    while ($true) {
        $ans = Read-Host "输入本次版本号（回车 = $defaultNext，次版本 +1；升主版本请输入，如 V2）"
        if (-not $ans) { $version = $defaultNext; break }
        $ans = $ans.Trim()
        if ($ans -notmatch '^[vV]?\d+(\.\d+){0,2}$') { Write-Host "  格式无效，示例: V1.0 / V2 / V2.3.1"; continue }
        $version = 'V' + ($ans -replace '^[vV]', '')
        break
    }
}
# 统一补足三段后写回（手填 V2.0 → V2.0.0），保证 VERSION 文件与产物命名一致
if ($version -match '^[vV]?(\d+)(?:\.(\d+))?(?:\.(\d+))?$') {
    $vmaj = [int]$Matches[1]
    $vmin = 0; $vpat = 0
    if (-not [string]::IsNullOrEmpty($Matches[2])) { $vmin = [int]$Matches[2] }
    if (-not [string]::IsNullOrEmpty($Matches[3])) { $vpat = [int]$Matches[3] }
    $version = "V$vmaj.$vmin.$vpat"
}
Set-Content -Path $verFile -Value $version -Encoding ASCII
$pkgVersion = $version -replace '^[vV]', ''
# 版本号统一为三段式（V0.2 → 0.2.0）：deb/rpm 规范要求 x.y.z，nfpm 会自动补零。
# 若不统一，裸二进制/ipk 会是 0.2、deb/rpm 是 0.2.0，同一版本出现两种写法。
# VERSION 文件本身仍保持两段（V0.2），这样「回车 +0.1」的自动递增才有效。
if (($pkgVersion -split '\.').Count -lt 3) { $pkgVersion = "$pkgVersion.0" }
# goversioninfo 字符串版本同样要求 x.y.z
$viVer = $pkgVersion
Write-Host "[0/6] 版本号: $version（上次 $lastVer，打包用 $pkgVersion）"
$ldflags = "-s -w -X main.version=$version"

# ---- Windows exe 图标 + 版本信息（goversioninfo 生成 .syso，随 go build 链入 PE 资源）----
$iconIco = Join-Path $PSScriptRoot 'packaging\icon.ico'
function Get-Goversioninfo {
    $cmd = Get-Command goversioninfo -ErrorAction SilentlyContinue
    if ($cmd) { return $cmd.Source }
    $p = Join-Path (go env GOPATH) 'bin\goversioninfo.exe'
    if (Test-Path $p) { return $p }
    go install github.com/josephspurrier/goversioninfo/cmd/goversioninfo@latest 2>$null
    if (Test-Path $p) { return $p }
    return $null
}
# FixedFileInfo 数字版本：取开头的点分数字段，不足补 0（如 0.47 → 0.47.0.0）
$verGroups = ([regex]::Match($pkgVersion, '^\d+(\.\d+)*')).Value -split '\.'
$verParts = @(0, 0, 0, 0)
for ($i = 0; $i -lt [Math]::Min(4, $verGroups.Count); $i++) { $verParts[$i] = [int]$verGroups[$i] }

Write-Host "[1/6] 构建前端..."
Push-Location web
npm run build
if ($LASTEXITCODE -ne 0) { throw "前端构建失败" }
Pop-Location

Write-Host "[2/6] 同步前端产物到 embed 目录..."
Remove-Item "server\internal\webui\dist" -Recurse -Force -ErrorAction SilentlyContinue
Copy-Item "web\dist" "server\internal\webui\dist" -Recurse
# 保留空目录占位：.gitkeep 入库保证 clone 后无前端产物也能编译（embed all:dist）
New-Item -ItemType File -Force -Path "server\internal\webui\dist\.gitkeep" | Out-Null

Write-Host "[3/6] 构建服务端（单二进制，内嵌前端）..."
# 产物按架构分文件夹；fnos/synology 为 NAS 套件目录；.tools（fnpack 缓存）/.stage（暂存）为构建缓存
$archDirs = @('windows-amd64', 'linux-amd64', 'linux-arm64', 'linux-armv7', 'linux-mipsle')
foreach ($d in ($archDirs + @('fnos', 'synology', '.tools', '.stage'))) {
    New-Item -ItemType Directory -Force -Path (Join-Path build $d) | Out-Null
}
# 清理上一轮可再生的裸二进制与 ipk（每次构建都会重新生成，避免 0.2 / 0.2.0 两种命名并存）；
# deb/rpm 保留——nfpm 未安装时不会重建，删掉就丢了
foreach ($d in $archDirs) {
    Get-ChildItem (Join-Path build $d) -File -ErrorAction SilentlyContinue |
        Where-Object { $_.Name -match '^cc-(agent|dashboard)-' -and ($_.Name -match '\.ipk$' -or $_.Name -match '(linux|windows)-') } |
        Remove-Item -Force -ErrorAction SilentlyContinue
}
# 清理旧版扁平布局遗留在 build 根目录的产物（迁移到分架构目录后的一次性清理）
Get-ChildItem build -File -ErrorAction SilentlyContinue |
    Where-Object { $_.Name -match '^cc-(agent|server|dashboard)-' } |
    Remove-Item -Force -ErrorAction SilentlyContinue
# 清理上一轮 fpk/spk 与暂存目录
Remove-Item build\fnos\*.fpk -Force -ErrorAction SilentlyContinue
Remove-Item build\synology\*.spk -Force -ErrorAction SilentlyContinue
Remove-Item build\.stage -Recurse -Force -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Force -Path build\.stage | Out-Null
$env:CGO_ENABLED = "0"
$gvSrv = Get-Goversioninfo
if ($gvSrv -and (Test-Path $iconIco)) {
    & $gvSrv -64 -o (Join-Path $PSScriptRoot 'server\cmd\master\resource_windows_amd64.syso') `
        -icon $iconIco -file-version "$viVer" -product-version "$viVer" `
        -ver-major $verParts[0] -ver-minor $verParts[1] -ver-patch $verParts[2] -ver-build $verParts[3] `
        -product-ver-major $verParts[0] -product-ver-minor $verParts[1] -product-ver-patch $verParts[2] -product-ver-build $verParts[3] `
        (Join-Path $PSScriptRoot 'packaging\versioninfo-server.json')
    if ($LASTEXITCODE -ne 0) { Write-Warning "服务端版本资源生成失败（exe 将无图标）" }
} elseif (-not $gvSrv) {
    Write-Warning "goversioninfo 不可用，Windows 服务端 exe 将无图标（go install github.com/josephspurrier/goversioninfo/cmd/goversioninfo@latest）"
}
Push-Location server
$serverTargets = @(
    @{ GOOS = "windows"; GOARCH = "amd64"; Out = "..\build\windows-amd64\cc-dashboard-$pkgVersion-windows-amd64.exe" },
    @{ GOOS = "linux"; GOARCH = "amd64"; Out = "..\build\linux-amd64\cc-dashboard-$pkgVersion-linux-amd64" },
    @{ GOOS = "linux"; GOARCH = "arm64"; Out = "..\build\linux-arm64\cc-dashboard-$pkgVersion-linux-arm64" },
    @{ GOOS = "linux"; GOARCH = "arm"; GOARM = "7"; Out = "..\build\linux-armv7\cc-dashboard-$pkgVersion-linux-armv7" }
)
foreach ($t in $serverTargets) {
    $env:GOOS = $t.GOOS
    $env:GOARCH = $t.GOARCH
    if ($t.GOARM) { $env:GOARM = $t.GOARM } else { Remove-Item env:GOARM -ErrorAction SilentlyContinue }
    go build -ldflags="$ldflags" -o $t.Out ./cmd/master
    if ($LASTEXITCODE -ne 0) { throw "服务端构建失败: $($t.GOOS)/$($t.GOARCH)" }
    Write-Host "  ✓ $($t.Out)"
}
Pop-Location

Write-Host "[4/6] 构建 Agent 矩阵..."
$gvAgent = Get-Goversioninfo
if ($gvAgent -and (Test-Path $iconIco)) {
    & $gvAgent -64 -o (Join-Path $PSScriptRoot 'agent-go\resource_windows_amd64.syso') `
        -icon $iconIco -file-version "$viVer" -product-version "$viVer" `
        -ver-major $verParts[0] -ver-minor $verParts[1] -ver-patch $verParts[2] -ver-build $verParts[3] `
        -product-ver-major $verParts[0] -product-ver-minor $verParts[1] -product-ver-patch $verParts[2] -product-ver-build $verParts[3] `
        (Join-Path $PSScriptRoot 'packaging\versioninfo-agent.json')
    if ($LASTEXITCODE -ne 0) { Write-Warning "Agent 版本资源生成失败（exe 将无图标）" }
} elseif (-not $gvAgent) {
    Write-Warning "goversioninfo 不可用，Windows Agent exe 将无图标"
}
Push-Location agent-go
$agentTargets = @(
    @{ GOOS = "windows"; GOARCH = "amd64"; Out = "..\build\windows-amd64\cc-agent-$pkgVersion-windows-amd64.exe" },
    @{ GOOS = "linux"; GOARCH = "amd64"; Out = "..\build\linux-amd64\cc-agent-$pkgVersion-linux-amd64" },
    @{ GOOS = "linux"; GOARCH = "arm64"; Out = "..\build\linux-arm64\cc-agent-$pkgVersion-linux-arm64" },
    @{ GOOS = "linux"; GOARCH = "arm"; GOARM = "7"; Out = "..\build\linux-armv7\cc-agent-$pkgVersion-linux-armv7" },
    # GOMIPS=softfloat 必须显式指定：OpenWrt mipsel_24kc（24Kc 无 FPU），默认 hardfloat 会 SIGILL
    @{ GOOS = "linux"; GOARCH = "mipsle"; GOMIPS = "softfloat"; Out = "..\build\linux-mipsle\cc-agent-$pkgVersion-linux-mipsle" }
)
foreach ($t in $agentTargets) {
    $env:GOOS = $t.GOOS
    $env:GOARCH = $t.GOARCH
    if ($t.GOARM) { $env:GOARM = $t.GOARM } else { Remove-Item env:GOARM -ErrorAction SilentlyContinue }
    if ($t.GOMIPS) { $env:GOMIPS = $t.GOMIPS } else { Remove-Item env:GOMIPS -ErrorAction SilentlyContinue }
    go build -ldflags="$ldflags" -o $t.Out .
    if ($LASTEXITCODE -ne 0) { throw "Agent 构建失败: $($t.GOOS)/$($t.GOARCH)" }
    Write-Host "  ✓ $($t.Out)"
}
Pop-Location

Write-Host "[5/6] 打包 Linux（ipk / deb / rpm，可选）..."
# 交叉编译环境到此结束：立即重置，否则下面 go run mkipk 会产出 Linux 二进制无法在本机执行
Remove-Item env:GOOS, env:GOARCH, env:GOARM, env:GOMIPS -ErrorAction SilentlyContinue
# ipk（OpenWrt）：使用 tools/mkipk 纯 Go 构建（包内含 init.d + postinst，装完自动注册开机自启）
$ipkList = @(
    @{ Bin = "linux-mipsle\cc-agent-$pkgVersion-linux-mipsle"; Arch = 'mipsel_24kc';      Dir = 'linux-mipsle' },
    @{ Bin = "linux-armv7\cc-agent-$pkgVersion-linux-armv7";   Arch = 'arm_cortex-a7';    Dir = 'linux-armv7' },
    @{ Bin = "linux-arm64\cc-agent-$pkgVersion-linux-arm64";   Arch = 'aarch64_cortex-a53'; Dir = 'linux-arm64' },
    @{ Bin = "linux-amd64\cc-agent-$pkgVersion-linux-amd64";   Arch = 'x86_64';          Dir = 'linux-amd64' }
)
foreach ($e in $ipkList) {
    $bin = Join-Path $PSScriptRoot "build\$($e.Bin)"
    if (Test-Path $bin) {
        Push-Location (Join-Path $PSScriptRoot 'tools\mkipk')
        # 应用图标（/usr/share/pixmaps/<pkg>.png），缺失则不传（mkipk 跳过）
        $ipkIcon = Join-Path $PSScriptRoot 'packaging\icon-256.png'
        $iconArg = @(); if (Test-Path $ipkIcon) { $iconArg = @('-icon', $ipkIcon) }
        go run . -bin $bin -arch $e.Arch -version "$pkgVersion" -out (Join-Path $PSScriptRoot "build\$($e.Dir)\cc-agent-$pkgVersion-$($e.Arch).ipk") @iconArg
        if ($LASTEXITCODE -ne 0) { Write-Warning "ipk 打包失败: $($e.Bin)" }
        Pop-Location
    }
}
# deb / rpm：需要 nfpm（未安装则跳过并提示）
if (Get-Command nfpm -ErrorAction SilentlyContinue) {
    $root = $PSScriptRoot
    $sp = Join-Path $root 'packaging\nfpm'
    # --- Agent 包（unit + postinstall 自动注册）---
    # 显式 UTF-8 读取：PS5.1 默认按 ANSI(GBK) 读无 BOM 文件会吞中文后的换行，破坏 YAML（T-15 实测）
    $tmpl = [IO.File]::ReadAllText((Join-Path $root 'nfpm-agent.yml'), [Text.Encoding]::UTF8)
    $pairs = @(
        @{ b = "cc-agent-$pkgVersion-linux-amd64"; a = 'amd64'; d = 'linux-amd64' },
        @{ b = "cc-agent-$pkgVersion-linux-arm64"; a = 'arm64'; d = 'linux-arm64' }
    )
    foreach ($p in $pairs) {
        $binPath = Join-Path $root "build\$($p.d)\$($p.b)"
        if (-not (Test-Path $binPath)) { continue }
        $cfg = $tmpl.Replace('{bin}', $binPath).Replace('{arch}', $p.a).Replace('{version}', $pkgVersion)
    $cfg = $cfg.Replace('{unit}', (Join-Path $root 'packaging\cc-agent.service'))
    $cfg = $cfg.Replace('{example}', (Join-Path $root 'packaging\cc-agent.config.example.json'))
    $cfg = $cfg.Replace('{icon}', (Join-Path $root 'packaging\icon-256.png'))
    $cfg = $cfg.Replace('{agent_postinst}', (Join-Path $sp 'agent-postinstall.sh'))
    $cfg = $cfg.Replace('{agent_prererm}', (Join-Path $sp 'agent-preremove.sh'))
    $cfg = $cfg.Replace('{agent_postrm}', (Join-Path $sp 'agent-postremove.sh'))
        $tmpCfg = Join-Path $env:TEMP "nfpm-agent-$($p.a).yml"
        Set-Content -Path $tmpCfg -Value $cfg -Encoding UTF8
        nfpm package --config $tmpCfg --packager deb --target (Join-Path $root "build\$($p.d)")
        if ($LASTEXITCODE -ne 0) { throw "nfpm deb 打包失败: $($p.b)" }
        nfpm package --config $tmpCfg --packager rpm --target (Join-Path $root "build\$($p.d)")
        if ($LASTEXITCODE -ne 0) { throw "nfpm rpm 打包失败: $($p.b)" }
    }
    # --- 服务端包（master deb/rpm，装完即启动）---
    $tmplS = [IO.File]::ReadAllText((Join-Path $root 'nfpm-server.yml'), [Text.Encoding]::UTF8)
    $sPairs = @(
        @{ b = "cc-dashboard-$pkgVersion-linux-amd64"; a = 'amd64'; d = 'linux-amd64' },
        @{ b = "cc-dashboard-$pkgVersion-linux-arm64"; a = 'arm64'; d = 'linux-arm64' }
    )
    foreach ($p in $sPairs) {
        $binPath = Join-Path $root "build\$($p.d)\$($p.b)"
        if (-not (Test-Path $binPath)) { continue }
        $cfg = $tmplS.Replace('{bin}', $binPath).Replace('{arch}', $p.a).Replace('{version}', $pkgVersion)
    $cfg = $cfg.Replace('{unit}', (Join-Path $root 'packaging\cc-dashboard.service'))
    $cfg = $cfg.Replace('{icon}', (Join-Path $root 'packaging\icon-256.png'))
    $cfg = $cfg.Replace('{server_postinst}', (Join-Path $sp 'server-postinstall.sh'))
    $cfg = $cfg.Replace('{server_prererm}', (Join-Path $sp 'server-preremove.sh'))
    $cfg = $cfg.Replace('{server_postrm}', (Join-Path $sp 'server-postremove.sh'))
        $tmpCfg = Join-Path $env:TEMP "nfpm-server-$($p.a).yml"
        Set-Content -Path $tmpCfg -Value $cfg -Encoding UTF8
        nfpm package --config $tmpCfg --packager deb --target (Join-Path $root "build\$($p.d)")
        if ($LASTEXITCODE -ne 0) { throw "nfpm deb 打包失败: $($p.b)" }
        nfpm package --config $tmpCfg --packager rpm --target (Join-Path $root "build\$($p.d)")
        if ($LASTEXITCODE -ne 0) { throw "nfpm rpm 打包失败: $($p.b)" }
    }
} else {
    Write-Host "  提示: 未检测到 nfpm，跳过 deb/rpm（安装: go install github.com/goreleaser/nfpm/v2/cmd/nfpm@latest）"
}

Write-Host "[6/6] 打包 NAS 套件（fnOS fpk / 群晖 spk）..."

$icon256 = Join-Path $PSScriptRoot 'packaging\icon-256.png'

function Copy-Template([string]$src, [string]$dst) {
    if (Test-Path $dst) { Remove-Item $dst -Recurse -Force }
    Copy-Item $src $dst -Recurse
}

# Windows 打出的 tar 成员权限为 0666，群晖生命周期脚本需要可执行位；
# 定向把 scripts/* 改成 0755 并重算 ustar 头部校验和
function Set-TarMemberMode {
    param([string]$Path, [string[]]$ExecPrefixes)
    $bytes = [IO.File]::ReadAllBytes($Path)
    $pos = 0
    while ($pos + 512 -le $bytes.Length) {
        $isZero = $true
        for ($i = 0; $i -lt 8; $i++) { if ($bytes[$pos + $i] -ne 0) { $isZero = $false; break } }
        if ($isZero) { break }
        $nameEnd = [Array]::IndexOf($bytes, [byte]0, $pos, 100)
        if ($nameEnd -lt 0) { $nameEnd = $pos + 100 }
        $name = [Text.Encoding]::UTF8.GetString($bytes, $pos, $nameEnd - $pos)
        $prefix = ''
        if ($bytes[$pos + 257] -eq ([byte][char]'u')) {
            $pEnd = [Array]::IndexOf($bytes, [byte]0, $pos + 345, 155)
            if ($pEnd -lt 0) { $pEnd = $pos + 345 + 155 }
            if ($pEnd -gt ($pos + 345)) { $prefix = [Text.Encoding]::UTF8.GetString($bytes, $pos + 345, $pEnd - ($pos + 345)) }
        }
        $full = if ($prefix) { "$prefix/$name" } else { $name }
        $sizeStr = ([Text.Encoding]::ASCII.GetString($bytes, $pos + 124, 12)).Trim([char]0, ' ')
        $size = 0
        if ($sizeStr) { $size = [Convert]::ToInt64($sizeStr, 8) }
        $typeflag = [char]$bytes[$pos + 156]
        $want = $false
        foreach ($p in $ExecPrefixes) {
            if ($full -eq $p -or $full.StartsWith($p)) { $want = $true; break }
        }
        if ($want -and $typeflag -ne '5') {
            $modeBytes = [Text.Encoding]::ASCII.GetBytes("0000755" + [char]0)
            [Array]::Copy($modeBytes, 0, $bytes, $pos + 100, 8)
            $sum = 0
            for ($i = 0; $i -lt 512; $i++) {
                $b = if ($i -ge 148 -and $i -lt 156) { [byte][char]' ' } else { $bytes[$pos + $i] }
                $sum += $b
            }
            # ustar 校验和以 6 位八进制 ASCII 存储（不是十进制！）
            $oct = [Convert]::ToString($sum, 8).PadLeft(6, '0')
            $chk = $oct + [char]0 + [char]' '
            $chkBytes = [Text.Encoding]::ASCII.GetBytes($chk)
            [Array]::Copy($chkBytes, 0, $bytes, $pos + 148, 8)
        }
        $pos += 512 + [Math]::Ceiling($size / 512.0) * 512
    }
    [IO.File]::WriteAllBytes($Path, $bytes)
}

# ---- fnOS fpk：优先用 PATH 里的 fnpack，否则下载官方 Windows 版缓存到 build\.tools ----
$fnpackExe = $null
$fnpackCmd = Get-Command fnpack -ErrorAction SilentlyContinue
if ($fnpackCmd) { $fnpackExe = $fnpackCmd.Source }
else {
    $fnpackCached = Join-Path $PSScriptRoot 'build\.tools\fnpack.exe'
    if (-not (Test-Path $fnpackCached)) {
        Write-Host "  下载 fnpack（官方打包工具）..."
        & curl.exe -sL --retry 2 --connect-timeout 15 -o $fnpackCached 'https://static2.fnnas.com/fnpack/fnpack-1.2.3-windows-amd64'
        if ($LASTEXITCODE -ne 0 -or -not (Test-Path $fnpackCached) -or (Get-Item $fnpackCached).Length -lt 1MB) {
            Remove-Item $fnpackCached -Force -ErrorAction SilentlyContinue
            $fnpackCached = $null
        }
    }
    if ($fnpackCached) { $fnpackExe = $fnpackCached }
}
if (-not $fnpackExe) {
    Write-Warning "fnpack 不可用，跳过 fpk（可手动下载: https://developer.fnnas.com/docs/cli/fnpack/ 放到 build\.tools\fnpack.exe 或加入 PATH）"
}

$fnosDir = Join-Path $PSScriptRoot 'build\fnos'
# fnOS 的 platform 只有 x86 / arm / all：amd64→x86，arm64→arm（fnOS 无 32 位版）
$fpkJobs = @(
    @{ App = 'cc-dashboard'; Platform = 'x86'; Bin = "build\linux-amd64\cc-dashboard-$pkgVersion-linux-amd64" },
    @{ App = 'cc-dashboard'; Platform = 'arm'; Bin = "build\linux-arm64\cc-dashboard-$pkgVersion-linux-arm64" },
    @{ App = 'cc-agent';     Platform = 'x86'; Bin = "build\linux-amd64\cc-agent-$pkgVersion-linux-amd64" },
    @{ App = 'cc-agent';     Platform = 'arm'; Bin = "build\linux-arm64\cc-agent-$pkgVersion-linux-arm64" }
)
if ($fnpackExe) {
    foreach ($j in $fpkJobs) {
        $binPath = Join-Path $PSScriptRoot $j.Bin
        if (-not (Test-Path $binPath)) { Write-Warning "  跳过 fpk $($j.App)/$($j.Platform)：缺少 $($j.Bin)"; continue }
        $stage = Join-Path $PSScriptRoot "build\.stage\fnos\$($j.App)-$($j.Platform)"
        Copy-Template (Join-Path $PSScriptRoot "packaging\fnos\$($j.App)") $stage
        # manifest 注入版本与平台
        $mf = Join-Path $stage 'manifest'
        $mfText = [IO.File]::ReadAllText($mf, [Text.Encoding]::UTF8)
        $mfText = $mfText.Replace('{VERSION}', $pkgVersion).Replace('{PLATFORM}', $j.Platform)
        [IO.File]::WriteAllText($mf, $mfText, [Text.UTF8Encoding]::new($false))
        # 二进制固定名，与 cmd/main 中的引用一致
        New-Item -ItemType Directory -Force -Path (Join-Path $stage 'app') | Out-Null
        Copy-Item $binPath (Join-Path $stage "app\$($j.App)") -Force
        # fnpack 校验 ICON.PNG / ICON_256.PNG 存在
        if (Test-Path $icon256) {
            Copy-Item $icon256 (Join-Path $stage 'ICON.PNG') -Force
            Copy-Item $icon256 (Join-Path $stage 'ICON_256.PNG') -Force
            if (Test-Path (Join-Path $stage 'app\ui')) {
                $uiImg = Join-Path $stage 'app\ui\images'
                New-Item -ItemType Directory -Force -Path $uiImg | Out-Null
                Copy-Item $icon256 (Join-Path $uiImg 'icon_64.png') -Force
                Copy-Item $icon256 (Join-Path $uiImg 'icon_256.png') -Force
            }
        }
        Push-Location $fnosDir
        try {
            & $fnpackExe build --directory $stage | Out-Host
            if ($LASTEXITCODE -ne 0) { Write-Warning "  fpk 打包失败: $($j.App)/$($j.Platform)"; continue }
            # fnpack 按 manifest 的 appname 命名输出（<appname>.fpk），改名带版本与平台
            $built = Join-Path $fnosDir "$($j.App).fpk"
            if (Test-Path $built) {
                $outName = "$($j.App)-$pkgVersion-$($j.Platform).fpk"
                Move-Item $built (Join-Path $fnosDir $outName) -Force
                Write-Host "  ✓ fnos\$outName"
            }
        } finally { Pop-Location }
    }
}

# ---- 群晖 spk：amd64→x86_64、arm64→armv8、armv7→armv7（见官方 Appendix A）----
$spkDir = Join-Path $PSScriptRoot 'build\synology'
$spkVersion = "$pkgVersion-1"   # 群晖 version 格式：x.y.z-build
$spkJobs = @(
    @{ App = 'cc-dashboard'; SpkArch = 'x86_64'; Bin = "build\linux-amd64\cc-dashboard-$pkgVersion-linux-amd64" },
    @{ App = 'cc-dashboard'; SpkArch = 'armv8';  Bin = "build\linux-arm64\cc-dashboard-$pkgVersion-linux-arm64" },
    @{ App = 'cc-dashboard'; SpkArch = 'armv7';  Bin = "build\linux-armv7\cc-dashboard-$pkgVersion-linux-armv7" },
    @{ App = 'cc-agent';     SpkArch = 'x86_64'; Bin = "build\linux-amd64\cc-agent-$pkgVersion-linux-amd64" },
    @{ App = 'cc-agent';     SpkArch = 'armv8';  Bin = "build\linux-arm64\cc-agent-$pkgVersion-linux-arm64" },
    @{ App = 'cc-agent';     SpkArch = 'armv7';  Bin = "build\linux-armv7\cc-agent-$pkgVersion-linux-armv7" }
)
foreach ($j in $spkJobs) {
    $binPath = Join-Path $PSScriptRoot $j.Bin
    if (-not (Test-Path $binPath)) { Write-Warning "  跳过 spk $($j.App)/$($j.SpkArch)：缺少 $($j.Bin)"; continue }
    $stage = Join-Path $PSScriptRoot "build\.stage\synology\$($j.App)-$($j.SpkArch)"
    Copy-Template (Join-Path $PSScriptRoot "packaging\synology\$($j.App)") $stage
    # 官方 Package Structure 中的 LICENSE 与套件图标（缺图标/脚本会被 DSM 判为“套件文件格式不正确”）
    Copy-Item (Join-Path $PSScriptRoot 'LICENSE') (Join-Path $stage 'LICENSE') -Force
    if (Test-Path $icon256) {
        Copy-Item $icon256 (Join-Path $stage 'PACKAGE_ICON_256.PNG') -Force
        Add-Type -AssemblyName System.Drawing -ErrorAction SilentlyContinue
        $srcImg = [System.Drawing.Image]::FromFile($icon256)
        $bmp64 = New-Object System.Drawing.Bitmap(64, 64)
        $gfx = [System.Drawing.Graphics]::FromImage($bmp64)
        $gfx.InterpolationMode = [System.Drawing.Drawing2D.InterpolationMode]::HighQualityBicubic
        $gfx.DrawImage($srcImg, 0, 0, 64, 64)
        $gfx.Dispose(); $srcImg.Dispose()
        $bmp64.Save((Join-Path $stage 'PACKAGE_ICON.PNG'), [System.Drawing.Imaging.ImageFormat]::Png)
        $bmp64.Dispose()
    }
    # package.tgz：程序本体（postinst 中 chmod +x）
    $pkgSrc = Join-Path $stage '_pkg'
    New-Item -ItemType Directory -Force -Path $pkgSrc | Out-Null
    Copy-Item $binPath (Join-Path $pkgSrc $j.App) -Force
    $pkgTgz = Join-Path $stage 'package.tgz'
    tar --format=ustar -czf $pkgTgz -C $pkgSrc .
    if ($LASTEXITCODE -ne 0) { Write-Warning "  package.tgz 失败: $($j.App)/$($j.SpkArch)"; continue }
    Remove-Item $pkgSrc -Recurse -Force
    # INFO 注入版本 / 架构 / package.tgz 校验和
    $infoPath = Join-Path $stage 'INFO'
    $md5 = (Get-FileHash $pkgTgz -Algorithm MD5).Hash.ToLower()
    $info = [IO.File]::ReadAllText($infoPath, [Text.Encoding]::UTF8)
    $info = $info.Replace('{VERSION}', $spkVersion).Replace('{ARCH}', $j.SpkArch).
        Replace('{CHECKSUM}', $md5).Replace('{PORT}', '8080')
    [IO.File]::WriteAllText($infoPath, $info, [Text.UTF8Encoding]::new($false))
    # 组装 .spk（未压缩 tar），成员对齐官方 Package Structure；随后把 scripts/* 补成 0755
    $outName = "$($j.App)-$pkgVersion-$($j.SpkArch).spk"
    $tmpSpk = Join-Path $stage '_out.spk'
    $spkMembers = @('INFO', 'package.tgz', 'scripts', 'conf', 'WIZARD_UIFILES', 'LICENSE')
    if (Test-Path (Join-Path $stage 'PACKAGE_ICON.PNG')) { $spkMembers += 'PACKAGE_ICON.PNG', 'PACKAGE_ICON_256.PNG' }
    Push-Location $stage
    tar --format=ustar -cf $tmpSpk @spkMembers
    $tarOk = $LASTEXITCODE -eq 0
    Pop-Location
    if (-not $tarOk) { Write-Warning "  spk 组装失败: $($j.App)/$($j.SpkArch)"; continue }
    Set-TarMemberMode -Path $tmpSpk -ExecPrefixes @('scripts/', 'scripts')
    Move-Item $tmpSpk (Join-Path $spkDir $outName) -Force
    Write-Host "  ✓ synology\$outName"
}

Remove-Item env:GOOS, env:GOARCH, env:GOARM, env:GOMIPS -ErrorAction SilentlyContinue
Write-Host ""
Write-Host "构建完成（版本 $version），产物列表:"
Get-ChildItem build -Recurse -File -ErrorAction SilentlyContinue |
    Where-Object { $_.FullName -notmatch '[\\/]\.(tools|stage)[\\/]' } |
    Sort-Object FullName |
    Select-Object @{ n = 'File'; e = { $_.FullName.Substring((Join-Path $PWD 'build').Length + 1) } },
        @{ n = 'MB'; e = { [math]::Round($_.Length / 1MB, 2) } } |
    Format-Table -AutoSize
