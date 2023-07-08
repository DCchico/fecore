use std::fs::{self, File};
use std::io::Read;

use tflite::ops::builtin::BuiltinOpResolver;
use tflite::{FlatBufferModel, InterpreterBuilder, Result};

use std::convert::Infallible;
use std::net::SocketAddr;
use hyper::{Body, Request, Response, Server};
use hyper::service::{make_service_fn, service_fn};

async fn process_req(req: Request<Body>) -> Result<Response<Body>> {
    let hdr = req.headers();
    if hdr.contains_key("tensor") { 
        let tp_val = hdr.get("tensor").unwrap();
        let tp_str = tp_val.to_str().unwrap();
        println!("Tensor Path: {}", tp_str);
        let inference_result = run_model(tp_str).await.unwrap();
        Ok(Response::new(Body::from(inference_result)))
    } else {
        Ok(Response::new(Body::from("Please specify TENSOR NAME\n")))
    }
}

// async fn shutdown_signal() {
//     // Wait for the CTRL+C signal
//     tokio::signal::ctrl_c()
//         .await
//         .expect("failed to install CTRL+C signal handler");
// }

#[tokio::main(flavor = "current_thread")]
async fn main() -> Result<()> {
    // For every connection, we must make a `Service` to handle all
    // incoming HTTP requests on said connection.
    //let make_svc = make_service_fn(|_conn| {
        // This is the `Service` that will handle the connection.
        // `service_fn` is a helper to convert a function that
        // returns a Response into a `Service`.
    //     async { Ok::<_, Infallible>(service_fn(process_req)) }
    // });

    // let addr = ([0, 0, 0, 0], 8080).into();

    // let server = Server::bind(&addr).serve(make_svc);
    // let graceful = server.with_graceful_shutdown(shutdown_signal());

    // println!("Listening on http://{}", addr);

    // graceful.await;

    let tp_str: &str = "birdx224x224x3.rgb";
    let inference_result = run_model(tp_str).await.unwrap();
    println!("{}", inference_result);

    Ok(())
}


async fn run_model(tensor_path: &str) -> Result<(String)> {

    // let tensor_height =  224;
    // let tensor_width  =  224;
    let model = FlatBufferModel::build_from_file("lite-model_aiy_vision_classifier_birds_V1_3.tflite")?;

    let resolver = BuiltinOpResolver::default();

    let builder = InterpreterBuilder::new(model, &resolver)?;
    let mut interpreter = builder.build()?;

    interpreter.allocate_tensors()?;

    let inputs = interpreter.inputs().to_vec();
    assert_eq!(inputs.len(), 1);

    let input_index = inputs[0];

    let outputs = interpreter.outputs().to_vec();
    assert_eq!(outputs.len(), 1);

    let output_index = outputs[0];

    let input_tensor = interpreter.tensor_info(input_index).unwrap();
    // assert_eq!(input_tensor.dims, vec![1, 224, 224, 3]);

    let output_tensor = interpreter.tensor_info(output_index).unwrap();
    // assert_eq!(output_tensor.dims, vec![1, 965]);

    let mut input_file = File::open(tensor_path)?;
    // for i in 0..965 {
        input_file.read_exact(interpreter.tensor_data_mut(input_index)?)?;

        interpreter.invoke()?;

        let output: &[u8] = interpreter.tensor_data(output_index)?;
        let guess = output.iter().enumerate().max_by(|x, y| x.1.cmp(y.1)).unwrap().0 - 1;
        //println!("Class with max likelyhood: {}", guess);
        // println!("{}: {:?}", 0, output);
        // assert_eq!(i, guess);
    // }
    Ok(guess.to_string())
    // return guess
}
