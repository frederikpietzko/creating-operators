package io.kops.webapp

import io.fabric8.kubernetes.client.KubernetesClient
import io.fabric8.kubernetes.client.KubernetesClientBuilder
import io.ktor.server.application.Application
import io.ktor.server.application.ApplicationStopped
import io.ktor.server.application.createApplicationPlugin
import io.ktor.server.application.hooks.MonitoringEvent
import io.ktor.server.application.install
import io.ktor.util.AttributeKey

fun Application.configureKubernetes() {
    install(Kubernetes)
}

val Application.kubernetesClient get() = attributes[AttributeKey<KubernetesClient>("KubernetesClient")]

val Kubernetes = createApplicationPlugin(name = "Kubernetes") {
    val client = KubernetesClientBuilder().build()
    application.attributes.put(AttributeKey<KubernetesClient>("KubernetesClient"), client)

    on(MonitoringEvent(ApplicationStopped)) {
        client.close()
    }
}
