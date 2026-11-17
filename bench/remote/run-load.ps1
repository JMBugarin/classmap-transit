# Runs on the separate load machine. Same order, warmup and k6 script as bench/run.sh.
param(
  [Parameter(Mandatory)] [string]$HostAddr,
  [Parameter(Mandatory)] [string]$Key,
  [string]$Out = "bench\raw\v2",
  [string]$Rate = "100",
  [string]$Duration = "600s",
  [string]$Warmup = "30s",
  [int]$Repeats = 3
)
$ErrorActionPreference = "Stop"
Set-Location (Join-Path $PSScriptRoot "..\..")
New-Item -ItemType Directory -Force -Path $Out | Out-Null
$ssh = @("-i", $Key, "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=accept-new", "ec2-user@$HostAddr")
$base = "http://${HostAddr}:18080"
$orders = @(@("uncached", "indexed", "cached"), @("indexed", "cached", "uncached"), @("cached", "uncached", "indexed"))

function Stamp { (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ") }

& ssh @ssh "env" | Out-File -Encoding utf8 "$Out\host-environment.txt"
& k6 version | Out-File -Encoding utf8 "$Out\k6-version.txt"
& git rev-parse HEAD | Out-File -Encoding utf8 "$Out\load-git-sha.txt"

try {
  for ($rep = 0; $rep -lt $Repeats; $rep++) {
    foreach ($v in $orders[$rep % 3]) {
      & ssh @ssh "variant $rep $v" | Out-File -Encoding utf8 "$Out\plan-$rep-$v.txt"
      if ($LASTEXITCODE -ne 0) { throw "variant $rep $v failed on host" }
      cmd /c "k6 run --quiet -e BASE_URL=$base -e RATE=$Rate -e DURATION=$Warmup --summary-export $Out\warmup-$rep-$v.json bench\k6.js > $Out\warmup-$rep-$v.txt 2>&1"
      Stamp | Out-File -Encoding utf8 "$Out\started-$rep-$v.txt"
      cmd /c "k6 run -e BASE_URL=$base -e RATE=$Rate -e DURATION=$Duration --summary-export $Out\k6-$rep-$v.json bench\k6.js > $Out\k6-$rep-$v.txt 2>&1"
      if ($LASTEXITCODE -ne 0) { cmd /c "echo k6 exit $LASTEXITCODE >> $Out\k6-$rep-$v.txt" }
      Stamp | Out-File -Encoding utf8 "$Out\finished-$rep-$v.txt"
      "$(Stamp) rep $rep $v done" | Tee-Object -Append -FilePath "$Out\progress.txt"
    }
  }
} finally {
  & ssh @ssh "stop" | Out-Null
}
