$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$appID = 'Goobers.Desktop'

function Register-GoobersShortcut(
    [string] $executable,
    [string] $programs = [Environment]::GetFolderPath('Programs')
) {
    if ([string]::IsNullOrEmpty($programs)) { throw 'No per-user Start Menu' }
    [IO.Directory]::CreateDirectory($programs) | Out-Null
    $shortcut = [IO.Path]::Combine($programs, 'Goobers.lnk')
    $shell = New-Object -ComObject WScript.Shell
    $folder = (New-Object -ComObject Shell.Application).NameSpace($programs)
    if ([IO.File]::Exists($shortcut)) {
        $link = $shell.CreateShortcut($shortcut)
        $item = $folder.ParseName('Goobers.lnk')
        if ($link.TargetPath -eq $executable -and
            $item.ExtendedProperty('System.AppUserModel.ID') -eq $appID) {
            return
        }
    }

    # WScript.Shell cannot set AppUserModelID. Use the Shell property store.
    if (-not ('GoobersShortcutIdentity' -as [type])) {
        Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;
public static class GoobersShortcutIdentity {
    [StructLayout(LayoutKind.Sequential)]
    public struct PropertyKey {
        public Guid Format;
        public uint Id;
    }
    [StructLayout(LayoutKind.Sequential)]
    public struct PropVariant {
        public ushort Type, Reserved1, Reserved2, Reserved3;
        public IntPtr Text, Unused;
    }
    [ComImport, Guid("886D8EEB-8CF2-4446-8D02-CDBA1DBDCF99"),
     InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
    interface IPropertyStore {
        void GetCount(out uint count);
        void GetAt(uint index, out PropertyKey key);
        void GetValue(ref PropertyKey key, out PropVariant value);
        void SetValue(ref PropertyKey key, ref PropVariant value);
        void Commit();
    }
    [DllImport("shell32.dll", CharSet = CharSet.Unicode, PreserveSig = false)]
    static extern void SHGetPropertyStoreFromParsingName(
        string path, IntPtr bindContext, uint flags, ref Guid iid,
        [MarshalAs(UnmanagedType.Interface)] out IPropertyStore store);
    public static void Set(string path, string appID) {
        Guid iid = typeof(IPropertyStore).GUID;
        IPropertyStore store;
        SHGetPropertyStoreFromParsingName(path, IntPtr.Zero, 2, ref iid, out store);
        var key = new PropertyKey {
            Format = new Guid("9F4C2855-9F79-4B39-A8D0-E1D42DE1D5F3"), Id = 5
        };
        var value = new PropVariant { Type = 31, Text = Marshal.StringToCoTaskMemUni(appID) };
        try {
            store.SetValue(ref key, ref value);
            store.Commit();
        } finally {
            Marshal.FreeCoTaskMem(value.Text);
            Marshal.ReleaseComObject(store);
        }
    }
}
'@
    }
    $temporary = [IO.Path]::Combine($programs, 'Goobers-' + [Guid]::NewGuid().ToString('N') + '.lnk')
    try {
        $link = $shell.CreateShortcut($temporary)
        $link.TargetPath = $executable
        $link.Arguments = ''
        $link.WorkingDirectory = [IO.Path]::GetDirectoryName($executable)
        $link.Description = 'Goobers'
        $link.Save()
        [GoobersShortcutIdentity]::Set($temporary, $appID)
        if ([IO.File]::Exists($shortcut)) {
            [IO.File]::Replace($temporary, $shortcut, $null)
        } else {
            [IO.File]::Move($temporary, $shortcut)
        }
    } finally {
        if ([IO.File]::Exists($temporary)) { [IO.File]::Delete($temporary) }
    }
}

try {
    if ($PSVersionTable.PSVersion.Major -ne 5 -or
        -not [Environment]::UserInteractive -or
        [Diagnostics.Process]::GetCurrentProcess().SessionId -eq 0) {
        exit 10
    }
    $null = [Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime]
    $null = [Windows.Data.Xml.Dom.XmlDocument, Windows.Data.Xml.Dom.XmlDocument, ContentType = WindowsRuntime]
} catch { exit 10 }

try {
    $payload = [Text.Encoding]::UTF8.GetString(
        [Convert]::FromBase64String([Console]::In.ReadToEnd())) | ConvertFrom-Json
    if (-not [IO.Path]::IsPathRooted($payload.Executable) -or
        -not [IO.File]::Exists($payload.Executable)) { exit 14 }
    $xml = New-Object Windows.Data.Xml.Dom.XmlDocument
    $xml.LoadXml($payload.XML)
} catch { exit 14 }

try { Register-GoobersShortcut $payload.Executable } catch { exit 11 }

try {
    $notifier = [Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier($appID)
    if ($notifier.Setting -ne [Windows.UI.Notifications.NotificationSetting]::Enabled) { exit 12 }
    $toast = [Windows.UI.Notifications.ToastNotification]::new($xml)
    $notifier.Show($toast)
} catch { exit 13 }
exit 0
