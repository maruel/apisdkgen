// Provides deterministic Linux URLSession byte transport for generated Swift tests.
import Foundation
@_exported import FoundationNetworking

// FoundationNetworking does not implement Apple's AsyncBytes API. Keep this
// shim at the network boundary: parser, dispatch, pull and reconnect are generated.
actor TransportSignal {
    private var signalled = false
    private var waiters: [CheckedContinuation<Void, Never>] = []
    func wait() async {
        if signalled { return }
        await withCheckedContinuation { waiters.append($0) }
    }
    func signal() {
        signalled = true
        let pending = waiters
        waiters.removeAll()
        for waiter in pending { waiter.resume() }
    }
}

// URLSessionTask.cancel updates FoundationNetworking's state asynchronously.
// Record the cancellation request itself at this transport boundary.
final class TransportTask: URLSessionDataTask, @unchecked Sendable {
    private let lock = NSLock()
    private var cancelled = false
    override func cancel() {
        lock.lock()
        cancelled = true
        lock.unlock()
    }
    override var state: URLSessionTask.State {
        lock.lock()
        defer { lock.unlock() }
        return cancelled ? .canceling : .suspended
    }
}

enum Transport {
    static var requests: [URLRequest] = []
    static var byteReads = 0
    static var task: URLSessionDataTask?
    static var normalCount = 0
    static var transportCount = 0
    static let openStarted = TransportSignal()
    static let releaseOpen = TransportSignal()
    static let readStarted = TransportSignal()
}

extension URLSession {
    struct AsyncBytes: AsyncSequence {
        typealias Element = UInt8
        let content: [UInt8]
        let task: URLSessionDataTask
        let idle: Bool
        let failAtEnd: Bool
        struct Iterator: AsyncIteratorProtocol {
            var content: [UInt8]
            let idle: Bool
            let failAtEnd: Bool
            var offset = 0
            mutating func next() async throws -> UInt8? {
                try _Concurrency.Task.checkCancellation()
                if idle {
                    await Transport.readStarted.signal()
                    try await _Concurrency.Task.sleep(nanoseconds: 10_000_000_000)
                }
                if failAtEnd && offset == content.count { throw URLError(.networkConnectionLost) }
                guard offset < content.count else { return nil }
                let byte = content[offset]
                offset += 1
                Transport.byteReads += 1
                return byte
            }
        }
        func makeAsyncIterator() -> Iterator { Iterator(content: content, idle: idle, failAtEnd: failAtEnd) }
    }
    func bytes(for request: URLRequest) async throws -> (AsyncBytes, URLResponse) {
        Transport.requests.append(request)
        let mode = URLComponents(url: request.url!, resolvingAgainstBaseURL: false)!.queryItems!.first!.value!
        let content: String
        switch mode {
        case "opening":
            await Transport.openStarted.signal()
            await Transport.releaseOpen.wait()
            content = "data: {\"kind\":\"cancelled\"}\n\n"
        case "read-failure": content = "data: {\"kind\":\"before-failure\"}\n\n"
        case "framing":
            content = "\u{FEFF}data:{\"kind\":\"bom\"}\r\n\r\n" +
                ": comment\r\nevent: unknown\r\ndata: not JSON\r\n\r\n" +
                "event: ready\rdata:\r\r" +
                "event:\ndata:{\"kind\":\ndata:\"雪\"}\n\n" +
                "data: {\"kind\":\"last\"}\r\n\r\n" +
                "data: {\"kind\":\"incomplete\"}\n"
        case "plain":
            content = "data:{\"kind\":\ndata:\"雪\"}\r\n\r\n" +
                "data: not JSON\n\n" + "data: {\"kind\":\"last\"}\n\n"
        case "large": content = "data: {\"kind\":\"large\",\"text\":\"" + String(repeating: "x", count: 9 * 1024 * 1024) + "\"}\n\n"
        case "escaped": content = "data: {\"kind\":\"large\",\"text\":\"" + String(repeating: "\\u003c", count: 2 * 1024 * 1024) + "\"}\n\n"
        case "oversized": content = String(repeating: "data: " + String(repeating: "x", count: 1024) + "\n", count: 5) + "\n"
        case "malformed": content = "event: checkpoint\ndata: {}\n\n"
        case "transport":
            Transport.transportCount += 1
            if Transport.transportCount == 1 { throw URLError(.networkConnectionLost) }
            content = "data: {\"kind\":\"recovered\"}\n\n"
        case "idle": content = ""
        default:
            Transport.normalCount += 1
            switch Transport.normalCount {
            case 1:
                content = "id: c1\r\nevent: checkpoint\r\ndata: {\"cursor\":\"c1\"}\r\n\r\n" +
                    "id: bad\0id\nevent: stats\ndata: {\"kind\":\"stats\"}\n\n" +
                    "event: reset\ndata: {\"bootstrapURL\":\"/bootstrap\"}\n\n" +
                    "event: error\ndata: {\"message\":\"recover\"}\n\n" +
                    "event: ready\rdata:\r\r" +
                    "data: {\"kind\":\ndata: \"one\"}\n\n"
            case 2: content = "id: id-only\n\ndata: {\"kind\":\"two\"}\n\n"
            case 3: content = "id:\n\ndata: {\"kind\":\"three\"}\n\n"
            default: content = "data: {\"kind\":\"four\"}\n\n"
            }
        }
        let task = TransportTask()
        Transport.task = task
        return (AsyncBytes(content: Array(content.utf8), task: task, idle: mode == "idle", failAtEnd: mode == "read-failure"),
                HTTPURLResponse(url: request.url!, statusCode: 200, httpVersion: nil, headerFields: nil)!)
    }
}
