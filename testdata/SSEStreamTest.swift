// Exercises generated Swift stream parsing, dispatch, reconnect and cancellation.
import Foundation

@main private struct SSEStreamTest {
    static func main() async throws {
        let client = ApiClient(baseURL: "https://example.test")
        var ready = 0
        var framed: [String] = []
        for try await event in client.events(cursor: "framing", onReady: { ready += 1 }) {
            framed.append(event.kind)
        }
        precondition(framed == ["bom", "雪", "last"] && ready == 1, "Frame boundaries must preserve dispatch")
        precondition(Transport.task?.state != .suspended, "EOF must release the response task")
        var plain: [String] = []
        for try await event in client.plainEvents(cursor: "plain") { plain.append(event.kind) }
        precondition(plain == ["雪", "last"], "Plain streams frame multiline data and discard decode failures")
        Transport.requests.removeAll()
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
        var failingRead = client.events(cursor: "read-failure").makeAsyncIterator()
        let beforeFailure = try await failingRead.next()
        precondition(beforeFailure?.kind == "before-failure")
        do { _ = try await failingRead.next(); preconditionFailure("Read failure did not terminate iteration") }
        catch { precondition(Transport.task?.state != .suspended, "Read failure must release the task") }
        try await consumeOne(client)
        precondition(Transport.task?.state != .suspended, "Stopping consumption must release the response task")
        let opening = _Concurrency.Task {
            var pending = client.events(cursor: "opening").makeAsyncIterator()
            _ = try await pending.next()
        }
        await Transport.openStarted.wait()
        opening.cancel()
        await Transport.releaseOpen.signal()
        do { _ = try await opening.value; preconditionFailure("Cancelled open delivered a value") }
        catch { precondition(Transport.task?.state != .suspended, "Pre-install cancellation must cancel the returned task") }
        let blocked = _Concurrency.Task {
            var idle = client.events(cursor: "idle").makeAsyncIterator()
            _ = try await idle.next()
        }
        await Transport.readStarted.wait()
        blocked.cancel()
        _ = await blocked.result
        precondition(Transport.task?.state != .suspended, "Cancellation must cancel the response task")
        print("PASS: generated Swift lossless pull, typed controls, ID-only/empty reset, EOF/transport reconnect, terminal decode and cancellation")
    }

    private static func consumeOne(_ client: ApiClient) async throws {
        var iterator: AsyncThrowingStream<testSDKEvent, Error>.Iterator? = client.events(cursor: "framing").makeAsyncIterator()
        _ = try await iterator?.next()
        iterator = nil
    }
}
