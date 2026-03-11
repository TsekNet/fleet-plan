Import-Module NVHelpers -Force -ErrorAction Stop
$app = 'cursor'
$uninstall_strings = Find-UninstallString -Name $app
foreach ($uninstall_string in $uninstall_strings) {
  $exit_code = Invoke-Binary $uninstall_string @('/VERYSILENT')
}
exit $exit_code
