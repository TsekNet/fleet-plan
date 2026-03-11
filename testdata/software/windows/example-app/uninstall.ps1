$app = Get-WmiObject -Class Win32_Product | Where-Object { $_.Name -match "Example App" }
if ($app) { $app.Uninstall() }
