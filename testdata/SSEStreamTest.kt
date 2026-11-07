// Exercises generated Kotlin HTTP completion, stream ownership, framing and reconnects.
package nativecheck

import com.sun.net.httpserver.HttpServer
import java.net.InetSocketAddress
import java.util.concurrent.CopyOnWriteArrayList
import java.util.concurrent.atomic.AtomicInteger
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.TimeoutCancellationException
import kotlinx.coroutines.cancelAndJoin
import kotlinx.coroutines.launch
import kotlinx.coroutines.withTimeout
import kotlinx.coroutines.yield
import kotlinx.coroutines.flow.buffer
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.flow
import kotlinx.coroutines.flow.collect
import kotlinx.coroutines.flow.onEach
import kotlinx.coroutines.flow.take
import kotlinx.coroutines.flow.toList
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import okhttp3.OkHttpClient
import okhttp3.Call
import okhttp3.EventListener
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.Protocol
import okhttp3.Response
import okhttp3.ResponseBody
import okhttp3.ResponseBody.Companion.toResponseBody
import okio.BufferedSource
import okio.Source
import okio.Timeout
import okio.buffer

fun main() = runBlocking {
    val requests = AtomicInteger()
    val ids = CopyOnWriteArrayList<String?>()
    val controls = CopyOnWriteArrayList<String>()
    val modeRequests = java.util.concurrent.ConcurrentHashMap<String, AtomicInteger>()
    val isolationIDs = CopyOnWriteArrayList<String?>()
    val server = HttpServer.create(InetSocketAddress("127.0.0.1", 0), 0)
    server.createContext("/events") { exchange ->
        val mode = exchange.requestURI.rawQuery.orEmpty()
        val attempt = modeRequests.computeIfAbsent(mode) { AtomicInteger() }.incrementAndGet()
        if (mode.startsWith("cursor=http-error")) {
            exchange.sendResponseHeaders(503, -1)
            exchange.close()
            return@createContext
        }
        if (mode.startsWith("cursor=transport-") && attempt == 1) {
            exchange.close()
            return@createContext
        }
        if (mode == "cursor=plain-eof" || mode.startsWith("cursor=transport-") || mode == "cursor=cursor-isolation") {
            exchange.responseHeaders.add("Content-Type", "text/event-stream")
            exchange.sendResponseHeaders(200, 0)
            if (mode == "cursor=cursor-isolation") isolationIDs.add(exchange.requestHeaders.getFirst("Last-Event-ID"))
            val content = when {
                mode == "cursor=plain-eof" && attempt == 1 -> ""
                mode == "cursor=cursor-isolation" -> "id: observed\ndata: {\"kind\":\"isolation\"}\n\n"
                else -> "data: {\"kind\":\"recovered\"}\n\n"
            }
            exchange.responseBody.use { it.write(content.toByteArray()) }
            exchange.close()
            return@createContext
        }
        if (mode == "cursor=burst") {
            exchange.responseHeaders.add("Content-Type", "text/event-stream")
            exchange.sendResponseHeaders(200, 0)
            try {
                exchange.responseBody.use { out ->
                    repeat(100) { out.write("data: {\"kind\":\"$it\"}\n\n".toByteArray()) }
                }
            } catch (_: java.io.IOException) {
                // take(1) deliberately closes the response before the server finishes.
            } finally { exchange.close() }
            return@createContext
        }
        if (mode == "cursor=framing") {
            exchange.responseHeaders.add("Content-Type", "text/event-stream")
            exchange.sendResponseHeaders(200, 0)
            val content = "\uFEFFdata:{\"kind\":\"bom\"}\r\n\r\n" +
                ": comment\r\nevent: unknown\r\ndata: not JSON\r\n\r\n" +
                "event: ready\rdata:\r\r" +
                "event:\ndata:{\"kind\":\ndata:\"雪\"}\n\n" +
                "data: {\"kind\":\"last\"}\r\n\r\n" +
                "data: {\"kind\":\"incomplete\"}\n"
            exchange.responseBody.use { out ->
                for (byte in content.toByteArray()) { out.write(byte.toInt()); out.flush() }
            }
            exchange.close()
            return@createContext
        }
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
    val cancelledCalls = AtomicInteger()
    val http = OkHttpClient.Builder().retryOnConnectionFailure(false).eventListener(object : EventListener() {
        override fun canceled(call: Call) { cancelledCalls.incrementAndGet() }
    }).build()
    try {
        val client = ApiClient("http://127.0.0.1:${server.address.port}", httpClient = http)
        verifyReconnectTiming(client)
        val afterEOF = withTimeout(3000) { client.plainEventsReconnecting("plain-eof").take(1).toList() }
        check(afterEOF.single().kind == "recovered" && modeRequests["cursor=plain-eof"]?.get() == 2)
        for (named in listOf(false, true)) {
            val mode = if (named) "transport-named" else "transport-plain"
            val stream = if (named) client.eventsReconnecting(mode) else client.plainEventsReconnecting(mode)
            check(withTimeout(3000) { stream.take(1).toList() }.single().kind == "recovered")
            check(modeRequests["cursor=$mode"]?.get() == 2)
            val rejected = if (named) "http-error-named" else "http-error-plain"
            try {
                withTimeout(3000) {
                    (if (named) client.eventsReconnecting(rejected) else client.plainEventsReconnecting(rejected)).toList()
                }
                error("HTTP rejection was swallowed")
            } catch (e: java.io.IOException) { check(e.message == "SSE connection failed: HTTP 503") }
            check(modeRequests["cursor=$rejected"]?.get() == 1) { "HTTP rejection was retried" }
        }
        val order = mutableListOf<String>()
        val isolated = client.eventsReconnecting("cursor-isolation", lastEventID = "seed", onEventID = { order.add("id:$it") })
        repeat(2) { withTimeout(3000) { isolated.onEach { order.add("message") }.take(1).toList() } }
        check(isolationIDs == listOf("seed", "seed")) { "Cursor leaked between collections: $isolationIDs" }
        check(order == listOf("id:observed", "message", "id:observed", "message"))
        val bodyFailure = java.io.IOException("body read failure")
        val bodyClosed = java.util.concurrent.atomic.AtomicBoolean()
        val readStarted = CompletableDeferred<Unit>()
        val callCancelled = CompletableDeferred<Boolean>()
        val cancelledBodyClosed = CompletableDeferred<Unit>()
        val releaseRead = java.util.concurrent.CountDownLatch(1)
        val failingHTTP = OkHttpClient.Builder().eventListener(object : EventListener() {
            override fun canceled(call: Call) {
                if (call.request().url.pathSegments.first() == "cancel") {
                    callCancelled.complete(call.isCanceled())
                    // Model a transport read that unblocks only when its Call is cancelled.
                    releaseRead.countDown()
                }
            }
        }).addInterceptor { chain ->
            val mode = chain.request().url.pathSegments.first()
            val body = if (mode == "read-failure" || mode == "cancel") {
                val source = object : Source {
                    override fun read(sink: okio.Buffer, byteCount: Long): Long {
                        if (mode == "cancel") { readStarted.complete(Unit); releaseRead.await() }
                        throw bodyFailure
                    }
                    override fun timeout() = Timeout.NONE
                    override fun close() {
                        bodyClosed.set(true)
                        if (mode == "cancel") cancelledBodyClosed.complete(Unit)
                    }
                }.buffer()
                object : ResponseBody() {
                    override fun contentType() = null
                    override fun contentLength() = -1L
                    override fun source(): BufferedSource = source
                }
            } else {
                val content = when (mode) {
                    "error" -> "{\"message\":\"server failure\"}"
                    "error-malformed" -> "not JSON"
                    else -> "{\"kind\":\"json\"}"
                }
                content.toResponseBody("application/json".toMediaType())
            }
            Response.Builder().request(chain.request()).protocol(Protocol.HTTP_1_1)
                .code(if (mode.startsWith("error")) 500 else 200).message("fixture").body(body).build()
        }.build()
        try {
            try {
                withTimeout(3000) { ApiClient("https://example.test/read-failure", httpClient = failingHTTP).getEvent() }
                error("Body-read failure did not reach caller")
            } catch (e: java.io.IOException) { check(e === bodyFailure && bodyClosed.get()) }
            check(ApiClient("https://example.test/valid", httpClient = failingHTTP).getEvent().kind == "json")
            for (mode in listOf("error", "error-malformed")) {
                try {
                    ApiClient("https://example.test/$mode", httpClient = failingHTTP).getEvent()
                    error("HTTP failure did not reach caller")
                } catch (e: ApiException) {
                    check(e.statusCode == 500 && e.message == if (mode == "error") "server failure" else "not JSON")
                }
            }
            val request = launch { ApiClient("https://example.test/cancel", httpClient = failingHTTP).getEvent() }
            withTimeout(3000) { readStarted.await(); request.cancelAndJoin() }
            withTimeout(3000) { check(callCancelled.await()); cancelledBodyClosed.await() }
        } finally {
            releaseRead.countDown()
            failingHTTP.dispatcher.executorService.shutdown()
            failingHTTP.connectionPool.evictAll()
        }
        val burst = withTimeout(3000) { client.plainEvents("burst").buffer(0).onEach { yield() }.toList() }
        check(burst.map { it.kind } == (0 until 100).map { it.toString() }) { "Backpressure lost events" }
        val cancellations = cancelledCalls.get()
        val firstOnly = withTimeout(3000) { client.plainEvents("burst").take(1).toList() }
        check(firstOnly.single().kind == "0" && cancelledCalls.get() > cancellations)
        try {
            withTimeout(3000) { client.plainEvents("malformed").toList() }
            error("Plain stream swallowed decode failure")
        } catch (_: kotlinx.serialization.SerializationException) { }
        val beforeMalformed = modeRequests["cursor=malformed"]?.get() ?: 0
        try {
            withTimeout(3000) { client.plainEventsReconnecting("malformed").toList() }
            error("Plain reconnect swallowed decode failure")
        } catch (_: kotlinx.serialization.SerializationException) { }
        check(modeRequests["cursor=malformed"]?.get() == beforeMalformed + 1)
        var ready = 0
        val framed = withTimeout(3000) { client.events("framing", onReady = { ready++ }).toList() }
        check(framed.map { it.kind } == listOf("bom", "雪", "last") && ready == 1) { "Frame boundaries lost dispatch: $framed" }
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
        val limited = ApiClient("http://127.0.0.1:${server.address.port}", httpClient = http, maxSSEEventBytes = 4096)
        for (reconnect in listOf(false, true)) {
            val before = modeRequests["cursor=large"]?.get() ?: 0
            try {
                withTimeout(3000) {
                    (if (reconnect) limited.eventsReconnecting("large") else limited.events("large")).toList()
                }
                error("Explicit line limit was ignored")
            } catch (e: java.io.IOException) { check(e.message == "SSE line too large") }
            check(modeRequests["cursor=large"]?.get() == before + 1) { "Size rejection was retried" }
        }
        try {
            withTimeout(3000) { client.events("malformed").toList() }
            error("Malformed checkpoint was swallowed")
        } catch (_: kotlinx.serialization.SerializationException) { }
        val beforeControl = modeRequests["cursor=malformed"]?.get() ?: 0
        try {
            withTimeout(3000) { client.eventsReconnecting("malformed").toList() }
            error("Reconnecting stream swallowed malformed checkpoint")
        } catch (_: kotlinx.serialization.SerializationException) { }
        check(modeRequests["cursor=malformed"]?.get() == beforeControl + 1)
        val failure = java.io.IOException("callback failure")
        val beforeCallback = modeRequests["cursor=callback"]?.get() ?: 0
        try {
            withTimeout(3000) { client.eventsReconnecting("callback", onCheckpoint = { throw failure }).toList() }
            error("Reconnecting stream swallowed callback failure")
        } catch (e: java.io.IOException) { check(e === failure) }
        check(modeRequests["cursor=callback"]?.get() == beforeCallback + 1) { "Callback failure was retried" }
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
        check(modeRequests["cursor=oversized"]?.get() == 1) { "Frame limit rejection was retried" }
        val started = System.nanoTime()
        try {
            withTimeout(300) { client.events("idle").toList() }
            error("Idle stream finished before cancellation")
        } catch (_: TimeoutCancellationException) { }
        check((System.nanoTime() - started) / 1_000_000 < 2000) { "Cancellation blocked" }
        println("PASS: generated Kotlin checkpoint -> controls -> multiline live events, reconnect ID header and empty reset")
    } finally {
        server.stop(0)
        http.dispatcher.executorService.shutdown()
        http.connectionPool.evictAll()
    }
}

@OptIn(kotlinx.coroutines.ExperimentalCoroutinesApi::class)
private fun verifyReconnectTiming(client: ApiClient) = runTest {
    // Keep runtime helpers private; invoke their real flow under the coroutine test clock.
    val method = ApiClient::class.java.getDeclaredMethod("reconnectingFlow", kotlin.Function0::class.java)
    method.isAccessible = true
    var attempts = 0
    val connect: () -> Flow<testSDKEvent> = {
        check(++attempts <= 4) { "Clean EOF hot loop" }
        flow { }
    }
    val reconnecting = method.invoke(client, connect) as Flow<*>
    val collector = backgroundScope.launch { reconnecting.collect { error("Unexpected event") } }
    runCurrent()
    check(attempts == 1) { "Clean EOF reconnected without backoff: $attempts attempts" }
    advanceTimeBy(374)
    runCurrent()
    check(attempts == 1) { "Retry preceded the minimum jittered delay" }
    collector.cancelAndJoin()
    advanceTimeBy(100_000)
    runCurrent()
    check(attempts == 1) { "Cancellation during backoff reconnected" }
    val times = mutableListOf<Long>()
    var connections = 0
    val scripted: () -> Flow<testSDKEvent> = {
        times.add(testScheduler.currentTime)
        val connection = ++connections
        flow {
            if (connection == 4) emit(testSDKEvent(kind = "reset"))
            if (connection >= 30) kotlinx.coroutines.awaitCancellation()
        }
    }
    val progressing = method.invoke(client, scripted) as Flow<*>
    val progress = backgroundScope.launch { progressing.collect { check((it as testSDKEvent).kind == "reset") } }
    runCurrent()
    repeat(30) { advanceTimeBy(40_000); runCurrent() }
    check(times.size == 30)
    check(times[1] - times[0] in 375L..625L)
    check(times[2] - times[1] in 562L..938L)
    check(times[3] - times[2] in 843L..1407L)
    check(times[4] - times[3] in 375L..625L) { "Delivered value did not reset backoff" }
    check(times.last() - times[times.lastIndex - 1] in 22_500L..37_500L) { "Backoff did not stop at its cap" }
    progress.cancelAndJoin()
}
