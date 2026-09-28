# Signs the given files with the Certum SimplySign certificate (selected by SHA1 thumbprint).
# Used by CI directly and by the NSIS installer (!finalize / !uninstfinalize in installer\project.nsi).
# Requires the SimplySign session to be authenticated (see .github/workflows/build.yml) and CERTUM_KEY_ID to be set.
param(
    [Parameter(Mandatory, ValueFromRemainingArguments)]
    [string[]]$Path
)

$ErrorActionPreference = 'Stop'

if (-not $env:CERTUM_KEY_ID) {
    throw 'CERTUM_KEY_ID (certificate SHA1 thumbprint) is not set.'
}

$signtool = Get-ChildItem -Path "${env:ProgramFiles(x86)}\Windows Kits\10\bin\*\x64\signtool.exe" |
    Sort-Object { [version]$_.Directory.Parent.Name } -Descending |
    Select-Object -First 1
if (-not $signtool) {
    throw 'signtool.exe not found in the Windows SDK.'
}

& $signtool.FullName sign /tr http://timestamp.certum.pl /td sha256 /fd sha256 /sha1 $env:CERTUM_KEY_ID $Path
if ($LASTEXITCODE -ne 0) {
    throw "signtool sign failed with exit code $LASTEXITCODE"
}
