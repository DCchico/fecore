#![deny(warnings)]

use std::convert::Infallible;

use hyper::service::{make_service_fn, service_fn};
use hyper::{Body, Request, Response, Server};

async fn hello(_: Request<Body>) -> Result<Response<Body>, Infallible> {
    Ok(Response::new(Body::from("WASM: Hello World!\n")))
}

#[tokio::main(flavor = "current_thread")]
async fn main() -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
    // pretty_env_logger::init();

    // For every connection, we must make a `Service` to handle all
    // incoming HTTP requests on said connection.
    let make_svc = make_service_fn(|_conn| {
        // This is the `Service` that will handle the connection.
        // `service_fn` is a helper to convert a function that
        // returns a Response into a `Service`.
        async { Ok::<_, Infallible>(service_fn(hello)) }
    });

    let addr = ([0, 0, 0, 0], 8080).into();

    let server = Server::bind(&addr).serve(make_svc);

    println!("Listening on http://{}", addr);

    server.await?;

    Ok(())
}

// use std::net::SocketAddr;

// use hyper::server::conn::Http;
// use hyper::service::service_fn;
// use hyper::{Body, Method, Request, Response, StatusCode};
// use tokio::net::TcpListener;

// /// This is our service handler. It receives a Request, routes on its
// /// path, and returns a Future of a Response.
// async fn echo(req: Request<Body>) -> Result<Response<Body>, hyper::Error> {
//     match (req.method(), req.uri().path()) {
//         // Serve some instructions at /
//         (&Method::GET, "/") => Ok(Response::new(Body::from(
//             "Try POSTing data to /echo such as: `curl localhost:8080/echo -XPOST -d 'hello world'`",
//         ))),

//         // Simply echo the body back to the client.
//         (&Method::POST, "/echo") => Ok(Response::new(req.into_body())),

//         (&Method::POST, "/echo/reversed") => {
//             let whole_body = hyper::body::to_bytes(req.into_body()).await?;

//             let reversed_body = whole_body.iter().rev().cloned().collect::<Vec<u8>>();
//             Ok(Response::new(Body::from(reversed_body)))
//         }

//         // Return the 404 Not Found for other routes.
//         _ => {
//             let mut not_found = Response::default();
//             *not_found.status_mut() = StatusCode::NOT_FOUND;
//             Ok(not_found)
//         }
//     }
// }

// #[tokio::main(flavor = "current_thread")]
// async fn main() -> Result<(), Box<dyn std::error::Error + Send + Sync>> {
//     let addr = SocketAddr::from(([0, 0, 0, 0], 8080));

//     let listener = TcpListener::bind(addr).await?;
//     println!("Listening on http://{}", addr);
//     loop {
//         let (stream, _) = listener.accept().await?;

//         tokio::task::spawn(async move {
//             if let Err(err) = Http::new().serve_connection(stream, service_fn(echo)).await {
//                 println!("Error serving connection: {:?}", err);
//             }
//         });
//     }
// }