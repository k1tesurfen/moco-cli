import Foundation

// Newline-delimited JSON over a Unix socket. The daemon (client) sends commands, the helper
// (server) answers with events. See internal/notify/notify.go for the Go side.

/// One button of a notification. `input` turns it into a text-reply action.
struct ActionSpec: Codable {
    let id: String
    let title: String
    var input: Bool?
    var placeholder: String?
    var button: String?
    var destructive: Bool?
}

/// Daemon → helper.
///   {"type":"ping"}
///   {"type":"notify","id":…,"category":…,"title":…,"subtitle"?:…,"body":…,"actions":[…],"sound"?:true}
///   {"type":"remove","ids":[…]}
///   {"type":"quit"}
struct Command: Codable {
    let type: String
    var id: String?
    var category: String?
    var title: String?
    var subtitle: String?
    var body: String?
    var actions: [ActionSpec]?
    var sound: Bool?
    var ids: [String]?
}

/// Helper → daemon.
///   {"type":"pong","version":…,"authorization":"authorized|denied|notDetermined|provisional"}
///   {"type":"delivered","id":…}
///   {"type":"response","id":…,"action":…,"text"?:…}   action "default" = notification clicked
///   {"type":"response","id":…,"dismissed":true}
///   {"type":"error","id"?:…,"message":…}
struct Event: Codable {
    let type: String
    var id: String?
    var action: String?
    var text: String?
    var dismissed: Bool?
    var authorization: String?
    var version: String?
    var message: String?
}
