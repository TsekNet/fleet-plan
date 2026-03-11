Import-Module NVHelpers -Force -ErrorAction Stop
$exit_code = Invoke-Binary $env:INSTALLER_PATH @('/VERYSILENT')
exit $exit_code
