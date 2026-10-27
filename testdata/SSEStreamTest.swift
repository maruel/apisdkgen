// Exercises generated Swift stream parsing, dispatch, reconnect and cancellation.
import Foundation

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
