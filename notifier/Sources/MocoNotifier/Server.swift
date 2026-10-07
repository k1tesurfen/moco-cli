import Foundation

/// A Unix socket server. Replies (pong, delivered, error) go to the client that asked; the user's
/// answers go to every connected client — each ignores ids it doesn't own. Answers given while no
/// client is connected are kept and delivered to the next one, so a click is not lost when the
/// daemon restarts.
final class Server {
    let path: String
    var onCommand: (Command, Int32) -> Void = { _, _ in }

    private let lock = NSLock()
    private var clients = Set<Int32>()
    private var backlog: [Data] = []
    private let maxBacklog = 100

    init(path: String) { self.path = path }

    func start() throws {
        let dir = (path as NSString).deletingLastPathComponent
        try FileManager.default.createDirectory(atPath: dir, withIntermediateDirectories: true,
                                                attributes: [.posixPermissions: 0o700])
        let fd = socket(AF_UNIX, SOCK_STREAM, 0)
        guard fd >= 0 else { throw posixError("socket") }

        var addr = sockaddr_un()
        addr.sun_family = sa_family_t(AF_UNIX)
        let bytes = Array(path.utf8CString)
        guard bytes.count <= MemoryLayout.size(ofValue: addr.sun_path) else {
            throw NSError(domain: "MocoNotifier", code: 1, userInfo: [NSLocalizedDescriptionKey: "socket path too long: \(path)"])
        }
        withUnsafeMutableBytes(of: &addr.sun_path) { buf in
            for (i, b) in bytes.enumerated() { buf[i] = UInt8(bitPattern: b) }
        }
        addr.sun_len = UInt8(MemoryLayout<sockaddr_un>.size)

        unlink(path) // a stale socket from a previous run
        let rc = withUnsafePointer(to: &addr) {
            $0.withMemoryRebound(to: sockaddr.self, capacity: 1) {
                bind(fd, $0, socklen_t(MemoryLayout<sockaddr_un>.size))
            }
        }
        guard rc == 0 else { throw posixError("bind \(path)") }
        chmod(path, 0o600)
        guard listen(fd, 8) == 0 else { throw posixError("listen") }

        let thread = Thread { [weak self] in self?.acceptLoop(fd) }
        thread.name = "accept"
        thread.start()
    }

    /// Sends a reply to one client.
    func send(_ event: Event, to fd: Int32) {
        guard let data = encode(event) else { return }
        lock.lock()
        defer { lock.unlock() }
        if clients.contains(fd) { _ = writeAll(fd, data) }
    }

    /// Sends a user's answer to every client, or keeps it for the next one.
    func broadcast(_ event: Event) {
        guard let data = encode(event) else { return }
        lock.lock()
        defer { lock.unlock() }
        var delivered = false
        for fd in clients where writeAll(fd, data) { delivered = true }
        if !delivered {
            backlog.append(data)
            if backlog.count > maxBacklog { backlog.removeFirst(backlog.count - maxBacklog) }
        }
    }

    private func encode(_ event: Event) -> Data? {
        guard var data = try? JSONEncoder().encode(event) else { return nil }
        data.append(0x0A)
        return data
    }

    private func acceptLoop(_ listener: Int32) {
        while true {
            let fd = accept(listener, nil, nil)
            if fd < 0 { continue }
            lock.lock()
            clients.insert(fd)
            var pending = backlog
            backlog.removeAll()
            while !pending.isEmpty, writeAll(fd, pending[0]) { pending.removeFirst() }
            backlog = pending
            lock.unlock()

            let thread = Thread { [weak self] in
                self?.readLoop(fd)
                self?.drop(fd)
            }
            thread.name = "client \(fd)"
            thread.start()
        }
    }

    private func drop(_ fd: Int32) {
        lock.lock()
        clients.remove(fd)
        lock.unlock()
        close(fd)
    }

    private func readLoop(_ fd: Int32) {
        var buffer = Data()
        var chunk = [UInt8](repeating: 0, count: 4096)
        while true {
            let n = read(fd, &chunk, chunk.count)
            if n <= 0 { return }
            buffer.append(contentsOf: chunk[0..<n])
            while let nl = buffer.firstIndex(of: 0x0A) {
                let line = buffer[buffer.startIndex..<nl]
                buffer.removeSubrange(buffer.startIndex...nl)
                if line.isEmpty { continue }
                do {
                    let cmd = try JSONDecoder().decode(Command.self, from: line)
                    DispatchQueue.main.async { self.onCommand(cmd, fd) }
                } catch {
                    send(Event(type: "error", message: "invalid command: \(error.localizedDescription)"), to: fd)
                }
            }
        }
    }

    private func writeAll(_ fd: Int32, _ data: Data) -> Bool {
        data.withUnsafeBytes { raw in
            var off = 0
            while off < raw.count {
                let n = write(fd, raw.baseAddress! + off, raw.count - off)
                if n <= 0 { return false }
                off += n
            }
            return true
        }
    }

    private func posixError(_ what: String) -> NSError {
        NSError(domain: NSPOSIXErrorDomain, code: Int(errno),
                userInfo: [NSLocalizedDescriptionKey: "\(what): \(String(cString: strerror(errno)))"])
    }
}
