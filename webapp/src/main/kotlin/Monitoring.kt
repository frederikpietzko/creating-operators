package io.kops.webapp

import com.sksamuel.cohort.Cohort
import com.sksamuel.cohort.HealthCheckRegistry
import com.sksamuel.cohort.cpu.ProcessCpuHealthCheck
import com.sksamuel.cohort.memory.FreememHealthCheck
import io.ktor.http.*
import io.ktor.server.application.*
import io.ktor.server.plugins.callid.*
import io.ktor.server.plugins.calllogging.*
import kotlinx.coroutines.Dispatchers
import kotlin.time.Duration.Companion.seconds

fun Application.configureMonitoring() {
    install(CallId) {
        header(HttpHeaders.XRequestId)
        verify { callId: String ->
            callId.isNotEmpty()
        }
    }

    val healthchecks = HealthCheckRegistry(Dispatchers.Default) {
        register(FreememHealthCheck.mb(250), 10.seconds, 10.seconds)
        register(ProcessCpuHealthCheck(0.8), 10.seconds, 10.seconds)
    }

    install(Cohort) {

        // enable an endpoint to display operating system name and version
        operatingSystem = true

        // enable runtime JVM information such as vm options and vendor name
        jvmInfo = true

        // show current system properties
        sysprops = true

        // enable an endpoint to dump the heap in hprof format
        heapDump = true

        // enable an endpoint to dump threads
        threadDump = true

        // set to true to return the detailed status of the healthcheck response
        verboseHealthCheckResponse = true

        // enable healthchecks for kubernetes
        healthcheck("/health", healthchecks)
    }
    install(CallLogging) {
        callIdMdc("call-id")
    }
}
