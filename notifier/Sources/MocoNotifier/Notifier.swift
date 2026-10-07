import Foundation
import UserNotifications

/// Shows notifications and reports what the user did with them. No business logic: the daemon
/// decides what to ask and what an answer means.
final class Notifier: NSObject, UNUserNotificationCenterDelegate {
    private let center = UNUserNotificationCenter.current()
    private let server: Server
    private var categories: [String: UNNotificationCategory] = [:]

    init(server: Server) {
        self.server = server
        super.init()
        center.delegate = self
    }

    func requestAuthorization() {
        center.requestAuthorization(options: [.alert, .sound]) { granted, error in
            if let error { NSLog("notification authorization: \(error.localizedDescription)") }
            NSLog("notifications \(granted ? "allowed" : "not allowed")")
        }
    }

    func handle(_ cmd: Command, from client: Int32) {
        switch cmd.type {
        case "ping":
            center.getNotificationSettings { settings in
                self.server.send(Event(type: "pong", authorization: Self.describe(settings.authorizationStatus), version: helperVersion), to: client)
            }
        case "notify":
            post(cmd, from: client)
        case "remove":
            let ids = cmd.ids ?? []
            center.removeDeliveredNotifications(withIdentifiers: ids)
            center.removePendingNotificationRequests(withIdentifiers: ids)
        case "quit":
            exit(0)
        default:
            server.send(Event(type: "error", id: cmd.id, message: "unknown command type \(cmd.type)"), to: client)
        }
    }

    private func post(_ cmd: Command, from client: Int32) {
        guard let id = cmd.id, let title = cmd.title else {
            server.send(Event(type: "error", id: cmd.id, message: "notify needs id and title"), to: client)
            return
        }
        let actions = cmd.actions ?? []
        let categoryID = Self.categoryID(cmd.category ?? "default", actions)
        if categories[categoryID] == nil {
            categories[categoryID] = UNNotificationCategory(
                identifier: categoryID,
                actions: actions.map(Self.action),
                intentIdentifiers: [],
                options: [.customDismissAction])
            center.setNotificationCategories(Set(categories.values))
        }

        let content = UNMutableNotificationContent()
        content.title = title
        if let subtitle = cmd.subtitle { content.subtitle = subtitle }
        content.body = cmd.body ?? ""
        content.categoryIdentifier = categoryID
        if cmd.sound ?? false { content.sound = .default }
        let request = UNNotificationRequest(identifier: id, content: content, trigger: nil)

        // Reading the categories back waits until the registration above is in effect;
        // otherwise the first notification of a new category can appear without its buttons.
        center.getNotificationCategories { _ in
            self.center.add(request) { error in
                if let error {
                    self.server.send(Event(type: "error", id: id, message: error.localizedDescription), to: client)
                } else {
                    self.server.send(Event(type: "delivered", id: id), to: client)
                }
            }
        }
    }

    private static func action(_ spec: ActionSpec) -> UNNotificationAction {
        let options: UNNotificationActionOptions = (spec.destructive ?? false) ? [.destructive] : []
        if spec.input ?? false {
            return UNTextInputNotificationAction(
                identifier: spec.id, title: spec.title, options: options,
                textInputButtonTitle: spec.button ?? "OK",
                textInputPlaceholder: spec.placeholder ?? "")
        }
        return UNNotificationAction(identifier: spec.id, title: spec.title, options: options)
    }

    /// A category per distinct action set, with a stable id (FNV-1a) so it survives restarts.
    private static func categoryID(_ name: String, _ actions: [ActionSpec]) -> String {
        var hash: UInt64 = 0xcbf29ce484222325
        for a in actions {
            for b in "\(a.id)\u{1}\(a.title)\u{1}\(a.input ?? false)\u{1}\(a.placeholder ?? "")\u{1}\(a.button ?? "")\u{2}".utf8 {
                hash = (hash ^ UInt64(b)) &* 0x100000001b3
            }
        }
        return "\(name).\(String(hash, radix: 16))"
    }

    private static func describe(_ status: UNAuthorizationStatus) -> String {
        switch status {
        case .authorized: return "authorized"
        case .denied: return "denied"
        case .notDetermined: return "notDetermined"
        case .provisional: return "provisional"
        case .ephemeral: return "ephemeral"
        @unknown default: return "unknown"
        }
    }

    // MARK: UNUserNotificationCenterDelegate

    func userNotificationCenter(_ center: UNUserNotificationCenter, willPresent notification: UNNotification,
                                withCompletionHandler completionHandler: @escaping (UNNotificationPresentationOptions) -> Void) {
        completionHandler([.banner, .list, .sound])
    }

    func userNotificationCenter(_ center: UNUserNotificationCenter, didReceive response: UNNotificationResponse,
                                withCompletionHandler completionHandler: @escaping () -> Void) {
        let id = response.notification.request.identifier
        var event = Event(type: "response", id: id)
        switch response.actionIdentifier {
        case UNNotificationDismissActionIdentifier:
            event.dismissed = true
        case UNNotificationDefaultActionIdentifier:
            event.action = "default"
        default:
            event.action = response.actionIdentifier
        }
        if let text = (response as? UNTextInputNotificationResponse)?.userText {
            event.text = text
        }
        server.broadcast(event)
        completionHandler()
    }
}
