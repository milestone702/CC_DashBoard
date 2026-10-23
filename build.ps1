# CC_DashBoard 一键构建脚本
# 用法: powershell -ExecutionPolicy Bypass -File build.ps1 [-Version V1.0] [-Auto]
param(
    [string]$Version = "",   # 直接指定本次版本号（如 -Version V2.0），跳过交互
    [switch]$Auto            # 非交互：自动 +0.1（CI / 脚本场景）
)
# 产物: build/ 目录（服务端三平台 + Agent 矩阵 + 前端已嵌入服务端 + ipk/deb/rpm）
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
    Write-Host "[0/5] 上次版本: $lastVer"
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
Write-Host "[0/5] 版本号: $version（上次 $lastVer，打包用 $pkgVersion）"
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

Write-Host "[1/5] 构建前端..."
Push-Location web
npm run build
if ($LASTEXITCODE -ne 0) { throw "前端构建失败" }
Pop-Location

Write-Host "[2/5] 同步前端产物到 embed 目录..."
Remove-Item "server\internal\webui\dist" -Recurse -Force -ErrorAction SilentlyContinue
Copy-Item "web\dist" "server\internal\webui\dist" -Recurse
# 保留空目录占位：.gitkeep 入库保证 clone 后无前端产物也能编译（embed all:dist）
New-Item -ItemType File -Force -Path "server\internal\webui\dist\.gitkeep" | Out-Null

Write-Host "[3/5] 构建服务端（单二进制，内嵌前端）..."
New-Item -ItemType Directory -Force -Path build | Out-Null
# 清理上一轮的裸二进制与 ipk（每次构建都会重新生成，避免 0.2 / 0.2.0 两种命名并存）；
# deb/rpm 保留——nfpm 未安装时不会重建，删掉就丢了
Get-ChildItem build -File -ErrorAction SilentlyContinue |
    Where-Object { $_.Name -match '^cc-(agent|server|dashboard)-' -and ($_.Name -match '\.ipk$' -or $_.Name -match '(linux|windows)-') } |
    Remove-Item -Force -ErrorAction SilentlyContinue
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
    @{ GOOS = "windows"; GOARCH = "amd64"; Out = "..\build\cc-dashboard-$pkgVersion-windows-amd64.exe" },
    @{ GOOS = "linux"; GOARCH = "amd64"; Out = "..\build\cc-dashboard-$pkgVersion-linux-amd64" },
    @{ GOOS = "linux"; GOARCH = "arm64"; Out = "..\build\cc-dashboard-$pkgVersion-linux-arm64" },
    @{ GOOS = "linux"; GOARCH = "arm"; GOARM = "7"; Out = "..\build\cc-dashboard-$pkgVersion-linux-armv7" }
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

Write-Host "[4/5] 构建 Agent 矩阵..."
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
    @{ GOOS = "windows"; GOARCH = "amd64"; Out = "..\build\cc-agent-$pkgVersion-windows-amd64.exe" },
    @{ GOOS = "linux"; GOARCH = "amd64"; Out = "..\build\cc-agent-$pkgVersion-linux-amd64" },
    @{ GOOS = "linux"; GOARCH = "arm64"; Out = "..\build\cc-agent-$pkgVersion-linux-arm64" },
    @{ GOOS = "linux"; GOARCH = "arm"; GOARM = "7"; Out = "..\build\cc-agent-$pkgVersion-linux-armv7" },
    # GOMIPS=softfloat 必须显式指定：OpenWrt mipsel_24kc（24Kc 无 FPU），默认 hardfloat 会 SIGILL
    @{ GOOS = "linux"; GOARCH = "mipsle"; GOMIPS = "softfloat"; Out = "..\build\cc-agent-$pkgVersion-linux-mipsle" }
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

Write-Host "[5/5] 打包（ipk / deb / rpm，可选）..."
# 交叉编译环境到此结束：立即重置，否则下面 go run mkipk 会产出 Linux 二进制无法在本机执行
Remove-Item env:GOOS, env:GOARCH, env:GOARM, env:GOMIPS -ErrorAction SilentlyContinue
# ipk（OpenWrt）：使用 tools/mkipk 纯 Go 构建（包内含 init.d + postinst，装完自动注册开机自启）
$ipkMap = [ordered]@{
    "cc-agent-$pkgVersion-linux-mipsle" = 'mipsel_24kc'
    "cc-agent-$pkgVersion-linux-armv7"  = 'arm_cortex-a7'
    "cc-agent-$pkgVersion-linux-arm64"  = 'aarch64_cortex-a53'
    "cc-agent-$pkgVersion-linux-amd64"  = 'x86_64'
}
foreach ($k in $ipkMap.Keys) {
    $bin = Join-Path $PSScriptRoot "build\$k"
    if (Test-Path $bin) {
        Push-Location (Join-Path $PSScriptRoot 'tools\mkipk')
        # 应用图标（/usr/share/pixmaps/<pkg>.png），缺失则不传（mkipk 跳过）
        $ipkIcon = Join-Path $PSScriptRoot 'packaging\icon-256.png'
        $iconArg = @(); if (Test-Path $ipkIcon) { $iconArg = @('-icon', $ipkIcon) }
        go run . -bin $bin -arch $ipkMap[$k] -version "$pkgVersion" -out (Join-Path $PSScriptRoot "build\cc-agent-$pkgVersion-$($ipkMap[$k]).ipk") @iconArg
        if ($LASTEXITCODE -ne 0) { Write-Warning "ipk 打包失败: $k" }
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
    $pairs = @(@{ b = "cc-agent-$pkgVersion-linux-amd64"; a = 'amd64' }, @{ b = "cc-agent-$pkgVersion-linux-arm64"; a = 'arm64' })
    foreach ($p in $pairs) {
        $binPath = Join-Path $root "build\$($p.b)"
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
        nfpm package --config $tmpCfg --packager deb --target (Join-Path $root 'build')
        if ($LASTEXITCODE -ne 0) { throw "nfpm deb 打包失败: $($p.b)" }
        nfpm package --config $tmpCfg --packager rpm --target (Join-Path $root 'build')
        if ($LASTEXITCODE -ne 0) { throw "nfpm rpm 打包失败: $($p.b)" }
    }
    # --- 服务端包（master deb/rpm，装完即启动）---
    $tmplS = [IO.File]::ReadAllText((Join-Path $root 'nfpm-server.yml'), [Text.Encoding]::UTF8)
    $sPairs = @(@{ b = "cc-dashboard-$pkgVersion-linux-amd64"; a = 'amd64' }, @{ b = "cc-dashboard-$pkgVersion-linux-arm64"; a = 'arm64' })
    foreach ($p in $sPairs) {
        $binPath = Join-Path $root "build\$($p.b)"
        if (-not (Test-Path $binPath)) { continue }
        $cfg = $tmplS.Replace('{bin}', $binPath).Replace('{arch}', $p.a).Replace('{version}', $pkgVersion)
    $cfg = $cfg.Replace('{unit}', (Join-Path $root 'packaging\cc-dashboard.service'))
    $cfg = $cfg.Replace('{icon}', (Join-Path $root 'packaging\icon-256.png'))
    $cfg = $cfg.Replace('{server_postinst}', (Join-Path $sp 'server-postinstall.sh'))
    $cfg = $cfg.Replace('{server_prererm}', (Join-Path $sp 'server-preremove.sh'))
    $cfg = $cfg.Replace('{server_postrm}', (Join-Path $sp 'server-postremove.sh'))
        $tmpCfg = Join-Path $env:TEMP "nfpm-server-$($p.a).yml"
        Set-Content -Path $tmpCfg -Value $cfg -Encoding UTF8
        nfpm package --config $tmpCfg --packager deb --target (Join-Path $root 'build')
        if ($LASTEXITCODE -ne 0) { throw "nfpm deb 打包失败: $($p.b)" }
        nfpm package --config $tmpCfg --packager rpm --target (Join-Path $root 'build')
        if ($LASTEXITCODE -ne 0) { throw "nfpm rpm 打包失败: $($p.b)" }
    }
} else {
    Write-Host "  提示: 未检测到 nfpm，跳过 deb/rpm（安装: go install github.com/goreleaser/nfpm/v2/cmd/nfpm@latest）"
}

Remove-Item env:GOOS, env:GOARCH, env:GOARM, env:GOMIPS -ErrorAction SilentlyContinue
Write-Host ""
Write-Host "构建完成（版本 $version），产物列表:"
Get-ChildItem build | Format-Table Name, @{ n = "MB"; e = { [math]::Round($_.Length / 1MB, 2) } } -AutoSize
