// Executes generated Swift stream parsing with a deterministic Linux byte transport.
import Foundation
@_exported import FoundationNetworking

// FoundationNetworking does not implement Apple's AsyncBytes API. Keep this
// shim at the network boundary: parser, dispatch, pull and reconnect are generated.
private enum Transport {
    static var requests: [URLRequest] = []
    static var byteReads = 0
    static var task: URLSessionDataTask?
    static var normalCount = 0
    static var transportCount = 0
}

extension URLSession {
    struct AsyncBytes: AsyncSequence {
        typealias Element = UInt8
        let content: [UInt8]
        let task: URLSessionDataTask
        let idle: Bool
        struct Iterator: AsyncIteratorProtocol {
            var content: [UInt8]
            let idle: Bool
            var offset = 0
            mutating func next() async throws -> UInt8? {
                try _Concurrency.Task.checkCancellation()
                if idle { try await _Concurrency.Task.sleep(nanoseconds: 10_000_000_000) }
                guard offset < content.count else { return nil }
                let byte = content[offset]
                offset += 1
                Transport.byteReads += 1
                return byte
            }
        }
        func makeAsyncIterator() -> Iterator { Iterator(content: content, idle: idle) }
        var lines: AsyncStream<String> { AsyncStream { $0.finish() } }
    }
    func bytes(for request: URLRequest) async throws -> (AsyncBytes, URLResponse) {
        Transport.requests.append(request)
        let mode = URLComponents(url: request.url!, resolvingAgainstBaseURL: false)!.queryItems!.first!.value!
        let content: String
        switch mode {
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
        let task = dataTask(with: request)
        Transport.task = task
        return (AsyncBytes(content: Array(content.utf8), task: task, idle: mode == "idle"),
                HTTPURLResponse(url: request.url!, statusCode: 200, httpVersion: nil, headerFields: nil)!)
    }
}

@main private struct SSEStreamTest {
    static func main() async throws {
        let client = ApiClient(baseURL: "https://example.test")
        var controls: [String] = []
        var iterator = client.eventsReconnecting(cursor: "normal",
            onCheckpoint: { controls.append("checkpoint:\($0.cursor)") },
            onStats: { controls.append("stats:\($0.kind)") },
            onReset: { controls.append("reset:\($0.bootstrapURL)") },
            onHistoryError: { controls.append("error:\($0.message)") },
            onReady: { controls.append("ready") }).makeAsyncIterator()
        precondition(Transport.requests.isEmpty, "Opening a stream must not start a producer")
        let first = try await iterator.next()
        precondition(first?.kind == "one")
        let readCount = Transport.byteReads
        try await _Concurrency.Task.sleep(nanoseconds: 30_000_000)
        precondition(Transport.byteReads == readCount, "Slow consumers must stop reads losslessly")
        for expected in ["two", "three", "four"] {
            let event = try await iterator.next()
            precondition(event?.kind == expected)
        }
        precondition(controls == ["checkpoint:c1", "stats:stats", "reset:/bootstrap", "error:recover", "ready"])
        precondition(Transport.requests.map { $0.value(forHTTPHeaderField: "Last-Event-ID") } == [nil, "c1", "id-only", ""])
        for mode in ["large", "escaped"] {
            var large = client.events(cursor: mode).makeAsyncIterator()
            let event = try await large.next()
            let expected = mode == "large" ? String(repeating: "x", count: 9 * 1024 * 1024) : String(repeating: "<", count: 2 * 1024 * 1024)
            precondition(event?.text == expected, "Large event must be delivered without truncation")
        }
        let limited = ApiClient(baseURL: "https://example.test", maxSSEEventBytes: 4096)
        for mode in ["large", "oversized"] {
            let count = Transport.requests.count
            var oversized = limited.eventsReconnecting(cursor: mode).makeAsyncIterator()
            do { _ = try await oversized.next(); preconditionFailure("Explicit size limit was ignored") }
            catch let error as URLError { precondition(error.code == .dataLengthExceedsMaximum) }
            precondition(Transport.requests.count == count + 1, "Size rejection must be terminal")
        }
        for reconnect in [false, true] {
            var malformed = (reconnect ? client.eventsReconnecting(cursor: "malformed") : client.events(cursor: "malformed")).makeAsyncIterator()
            do { _ = try await malformed.next(); preconditionFailure("Malformed control without callback was swallowed") }
            catch is DecodingError { }
        }
        var recovered = client.eventsReconnecting(cursor: "transport").makeAsyncIterator()
        let value = try await recovered.next()
        precondition(value?.kind == "recovered" && Transport.transportCount == 2)
        let blocked = _Concurrency.Task {
            var idle = client.events(cursor: "idle").makeAsyncIterator()
            _ = try await idle.next()
        }
        try await _Concurrency.Task.sleep(nanoseconds: 30_000_000)
        blocked.cancel()
        _ = await blocked.result
        precondition(Transport.task?.state != .suspended, "Cancellation must cancel the response task")
        print("PASS: generated Swift lossless pull, typed controls, ID-only/empty reset, EOF/transport reconnect, terminal decode and cancellation")
    }
}
