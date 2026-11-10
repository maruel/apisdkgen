// Exercises typed URL queries independently of JSON bodies and SSE responses.
import Foundation
@_exported import FoundationNetworking

// Query fixtures are bounded single-event responses; lifecycle tests use SSETransport.swift.
extension URLSession {
    struct AsyncBytes: AsyncSequence {
        typealias Element = UInt8
        let data: Data
        let task: URLSessionDataTask
        struct Iterator: AsyncIteratorProtocol {
            var iterator: Data.Iterator
            mutating func next() async throws -> UInt8? { iterator.next() }
        }
        func makeAsyncIterator() -> Iterator { Iterator(iterator: data.makeIterator()) }
    }
    func bytes(for request: URLRequest) async throws -> (AsyncBytes, URLResponse) {
        try await withCheckedThrowingContinuation { continuation in
            var task: URLSessionDataTask?
            task = dataTask(with: request) { data, response, error in
                if let error { continuation.resume(throwing: error); return }
                guard let data, let response, let task else { continuation.resume(throwing: URLError(.badServerResponse)); return }
                continuation.resume(returning: (AsyncBytes(data: data, task: task), response))
            }
            task?.resume()
        }
    }
}

@main private struct QueryClient {
    static func main() async throws {
        let client = ApiClient(baseURL: CommandLine.arguments[1])
        let query = testQueryInputQuery(limit: 0, unread: false, text: "a +&雪", ids: "a,b", maybe: nil, extra: "ignored")
        let first = try await client.list(queryPath: "full", query: query)
        precondition(first.kind == "listed")
        let blank = try await client.list(queryPath: "blank", query: testQueryInputQuery(limit: 0, unread: false, text: "", ids: "a,b", maybe: nil, extra: "ignored"))
        precondition(blank.kind == "listed")
        let empty = try await client.list(queryPath: "empty", query: testQueryInputQuery(limit: nil, unread: nil, text: nil, ids: nil, maybe: nil, extra: "ignored"))
        precondition(empty.kind == "listed")
        let updated = try await client.update(query: query, req: testSDKEvent(kind: "request", text: nil), headers: ["X-Test": "expected"])
        precondition(updated.kind == "listed")
        let reused = try await client.reuse(query: query, req: testQueryInput(limit: nil, unread: nil, text: nil, ids: nil, extra: "body"))
        precondition(reused.kind == "listed")
        let nilList = try await client.rootList(req: nil)
        let nilMap = try await client.rootMap(req: nil)
        let nilArray = try await client.rootArray()
        precondition(nilList == nil && nilMap == nil && nilArray == nil)
        let queryOnlyEmpty = try await client.queryOnlyEmpty(query: testStructOnlyQueryQuery())
        let queryOnlyValue = try await client.queryOnlyValue(query: testStructOnlyQueryQuery(since: "x"))
        precondition(queryOnlyEmpty.kind == "listed" && queryOnlyValue.kind == "listed")
        let headerBytes = try await client.rootBytesHeaders(headers: ["X-Test": "expected"])
        precondition(headerBytes == Data([0,1,255]))
        let bytes = try await client.rootBytes(req: Data([0,1,255]))
        let nestedBytes = try await client.rootNestedBytes(req: [Data([0,1,255]), nil])
        precondition(bytes == Data([0,1,255]) && nestedBytes?[0] == Data([0,1,255]) && nestedBytes?[1] == nil)
        let mappedBytes = try await client.rootBytesMap(req: ["x": Data([0,1,255]), "empty": nil])
        precondition(mappedBytes?["x"]! == Data([0,1,255]) && mappedBytes?.keys.contains("empty") == true)
        let emptyList = try await client.rootList(req: [])
        let emptyMap = try await client.rootMap(req: [:])
        precondition(emptyList?.isEmpty == true && emptyMap?.isEmpty == true)
        for stream in [client.events(query: query), client.eventsReconnecting(query: query), client.namedEvents(query: query), client.namedEventsReconnecting(query: query)] {
            var iterator = stream.makeAsyncIterator()
            let event = try await iterator.next()
            precondition(event?.kind == "listed")
        }
    }
}
