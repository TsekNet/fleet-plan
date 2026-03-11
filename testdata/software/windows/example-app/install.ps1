$installer = "$env:INSTALLER_PATH"
Start-Process msiexec.exe -ArgumentList "/i `"$installer`" /qn /norestart" -Wait
