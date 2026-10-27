// Exercises generated Kotlin named SSE dispatch and reconnect framing over HTTP.
package nativecheck

import com.sun.net.httpserver.HttpServer
import java.net.InetSocketAddress
import java.util.concurrent.CopyOnWriteArrayList
import java.util.concurrent.atomic.AtomicInteger
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.TimeoutCancellationException
import kotlinx.coroutines.withTimeout
import kotlinx.coroutines.flow.take
import kotlinx.coroutines.flow.toList

fun main() = runBlocking {
    val requests = AtomicInteger()
    val ids = CopyOnWriteArrayList<String?>()
    val controls = CopyOnWriteArrayList<String>()
    val server = HttpServer.create(InetSocketAddress("127.0.0.1", 0), 0)
    server.createContext("/events") { exchange ->
        val mode = exchange.requestURI.rawQuery
        if (mode == "cursor=large" || mode == "cursor=escaped") {
            exchange.responseHeaders.add("Content-Type", "text/event-stream")
            exchange.sendResponseHeaders(200, 0)
            val text = if (mode == "cursor=large") "x".repeat(9 * 1024 * 1024) else "\\u003c".repeat(2 * 1024 * 1024)
            try {
                exchange.responseBody.use { it.write(("data: {\"kind\":\"large\",\"text\":\"" + text + "\"}\n\n").toByteArray()) }
            } catch (_: java.io.IOException) {
                // An explicitly size-limited client can cancel before the server finishes writing.
            } finally { exchange.close() }
            return@createContext
        }
        if (mode == "cursor=idle") {
            exchange.responseHeaders.add("Content-Type", "text/event-stream")
            exchange.sendResponseHeaders(200, 0)
            exchange.responseBody.write(": waiting\n\n".toByteArray())
            exchange.responseBody.flush()
            return@createContext
        }
        if (mode == "cursor=malformed" || mode == "cursor=callback" || mode == "cursor=oversized") {
            exchange.responseHeaders.add("Content-Type", "text/event-stream")
            exchange.sendResponseHeaders(200, 0)
            try {
                exchange.responseBody.use {
                    if (mode == "cursor=malformed") {
                        it.write("event: checkpoint\ndata: {}\n\n".toByteArray())
                    } else if (mode == "cursor=callback") {
                        it.write("id: callback-id\nevent: checkpoint\ndata: {\"cursor\":\"valid\"}\n\n".toByteArray())
                    } else {
                        val chunk = ("data: " + "x".repeat(1024) + "\n").toByteArray()
                        repeat(8193) { _ -> it.write(chunk) }
                    }
                }
            } catch (_: java.io.IOException) {
                // An explicitly size-limited parser cancels its HTTP call when it rejects the frame.
            } finally { exchange.close() }
            return@createContext
        }
        val n = requests.incrementAndGet()
        check(exchange.requestURI.rawQuery == "cursor=a+%2B%26%3D%23%25%E9%9B%AA")
        ids.add(exchange.requestHeaders.getFirst("Last-Event-ID"))
        exchange.responseHeaders.add("Content-Type", "text/event-stream")
        exchange.sendResponseHeaders(200, 0)
        val frame = when (n) {
            1 -> "id: c1\r\nevent: checkpoint\r\ndata: {\"cursor\":\"c1\"}\r\n\r\n" +
                "id: ignored\u0000id\nevent: stats\ndata: {\"kind\":\"stats\"}\n\n" +
                "event: unknown\ndata: not JSON\n\n" +
                "event: reset\ndata: {\"bootstrapURL\":\"/bootstrap\"}\n\n" +
                "event: error\ndata: {\"message\":\"recover\"}\n\n" +
                "event: ready\rdata:\r\r" +
                "data: {\"kind\":\ndata: \"one\"}\n\n"
            2 -> "id: id-only\n\n" +
                "data: {\"kind\":\"two\"}\n\n"
            3 -> "id:\n\n" + "data: {\"kind\":\"three\"}\n\n"
            else -> "data: {\"kind\":\"four\"}\n\n"
        }
        exchange.responseBody.use { it.write(frame.toByteArray()) }
        exchange.close()
    }
    server.start()
    try {
        val client = ApiClient("http://127.0.0.1:${server.address.port}")
        val messages = withTimeout(10000) {
            client.eventsReconnecting("a +&=#%雪",
                onCheckpoint = { controls.add("checkpoint:${it.cursor}") },
                onStats = { controls.add("stats:${it.kind}") },
                onReset = { controls.add("reset:${it.bootstrapURL}") },
                onHistoryError = { controls.add("error:${it.message}") },
                onReady = { controls.add("ready") }).take(4).toList()
        }
        check(messages.map { it.kind } == listOf("one", "two", "three", "four"))
        check(controls == listOf("checkpoint:c1", "stats:stats", "reset:/bootstrap", "error:recover", "ready"))
        check(ids == listOf(null, "c1", "id-only", "")) { "IDs: $ids" }
        for (mode in listOf("large", "escaped")) {
            val events = withTimeout(10000) { client.events(mode).take(1).toList() }
            val expected = if (mode == "large") "x".repeat(9 * 1024 * 1024) else "<".repeat(2 * 1024 * 1024)
            check(events.single().text == expected) { "Large decoded event was truncated" }
        }
        val limited = ApiClient("http://127.0.0.1:${server.address.port}", maxSSEEventBytes = 4096)
        for (reconnect in listOf(false, true)) {
            try {
                withTimeout(3000) {
                    (if (reconnect) limited.eventsReconnecting("large") else limited.events("large")).toList()
                }
                error("Explicit line limit was ignored")
            } catch (e: java.io.IOException) { check(e.message == "SSE line too large") }
        }
        try {
            withTimeout(3000) { client.events("malformed").toList() }
            error("Malformed checkpoint was swallowed")
        } catch (_: kotlinx.serialization.SerializationException) { }
        try {
            withTimeout(3000) { client.eventsReconnecting("malformed").toList() }
            error("Reconnecting stream swallowed malformed checkpoint")
        } catch (_: kotlinx.serialization.SerializationException) { }
        val failure = java.io.IOException("callback failure")
        try {
            withTimeout(3000) { client.eventsReconnecting("callback", onCheckpoint = { throw failure }).toList() }
            error("Reconnecting stream swallowed callback failure")
        } catch (e: java.io.IOException) { check(e === failure) }
        for (reconnect in listOf(false, true)) for (cancelFromID in listOf(false, true)) {
            val cancellation = CancellationException("callback stop")
            try {
                withTimeout(3000) {
                    val flow = if (reconnect) {
                        client.eventsReconnecting("callback",
                            onCheckpoint = { if (!cancelFromID) throw cancellation },
                            onEventID = { if (cancelFromID) throw cancellation })
                    } else {
                        client.events("callback",
                            onCheckpoint = { if (!cancelFromID) throw cancellation },
                            onEventID = { if (cancelFromID) throw cancellation })
                    }
                    flow.toList()
                }
                error("Callback cancellation did not terminate collection")
            } catch (e: CancellationException) { check(e === cancellation) { "Unexpected cancellation: $e" } }
        }
        try {
            withTimeout(3000) { limited.eventsReconnecting("oversized").toList() }
            error("Oversized frame was accepted")
        } catch (e: java.io.IOException) { check(e.message == "SSE frame too large") }
        val started = System.nanoTime()
        try {
            withTimeout(300) { client.events("idle").toList() }
            error("Idle stream finished before cancellation")
        } catch (_: TimeoutCancellationException) { }
        check((System.nanoTime() - started) / 1_000_000 < 2000) { "Cancellation blocked" }
        println("PASS: generated Kotlin checkpoint -> controls -> multiline live events, reconnect ID header and empty reset")
    } finally { server.stop(0) }
}
