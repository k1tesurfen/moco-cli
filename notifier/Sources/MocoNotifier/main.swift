// MocoNotifier is the notification helper of moco-cli: an agent app (no Dock icon, no windows)
// that shows notifications for the moco daemon and reports the answers back over a Unix socket.
import AppKit

let helperVersion = "1"

final class AppDelegate: NSObject, NSApplicationDelegate {
    let server: Server
    var notifier: Notifier?

    init(socketPath: String) { server = Server(path: socketPath) }

    func applicationWillFinishLaunching(_ notification: Notification) {
        // The delegate must be in place before launch finishes: a click on a notification can be
        // what launched the app.
        let n = Notifier(server: server)
        server.onCommand = { n.handle($0) }
        notifier = n
    }

    func applicationDidFinishLaunching(_ notification: Notification) {
        do {
            try server.start()
        } catch {
            NSLog("MocoNotifier: \(error.localizedDescription)")
            exit(1)
        }
        notifier?.requestAuthorization()
    }
}

func socketPath() -> String {
    let args = CommandLine.arguments
    if let i = args.firstIndex(of: "--socket"), i + 1 < args.count { return args[i + 1] }
    var base = ProcessInfo.processInfo.environment["XDG_STATE_HOME"] ?? ""
    if base.isEmpty { base = NSHomeDirectory() + "/.local/state" }
    return base + "/moco/notifier.sock"
}

signal(SIGPIPE, SIG_IGN)
let app = NSApplication.shared
let delegate = AppDelegate(socketPath: socketPath())
app.delegate = delegate
app.setActivationPolicy(.accessory)
app.run()
